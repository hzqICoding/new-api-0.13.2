"""Fixed-baseline security rebuild rollout; no business configuration changes."""
import copy, fcntl, hashlib, http.cookiejar, importlib.util, json, os, pathlib, secrets, shutil, sys, tarfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT=pathlib.Path('/opt/carpool/releases/security-go12610-20261022')
FOLDER=ROOT/'new-api'
RELEASE=ROOT.name
VERSION='v0.13.2-carpool-r15.20-go12610-20261022'
OLD_IMAGE='sha256:608c40569380ef11b7d8038df04b36c3382e89553d2a21b0d70e059c2d4ad956'
OLD_BINARY='7411f485c5f152d311ab2dbc999a2c415b4acfe6dd5f477fcfb11bee8aba0870'
BINARY='97e241c4ce36ea36d1c361296beff02e0606e3bbc394971bb231297b1c028e02'
ARCHIVE='166a09ab98ba206be2172e3c4def87cf2cdc662db51b17cbfbbe99c312850c22'
IMAGE='local/new-api:go12610-20261022'
spec=importlib.util.spec_from_file_location('resource_guard','/opt/carpool/releases/resource-guard-20261021/rollout.py')
guard=importlib.util.module_from_spec(spec);spec.loader.exec_module(guard)
guard.ROOT=ROOT;guard.RELEASE=RELEASE;guard.LABEL='carpool.security-release'
ops=guard.ops
sys.path.insert(0,'/opt/carpool/releases/r15-20260906')
from extract_embedded_frontend import recover
os.umask(0o077)

def build_version(path):
    b=pathlib.Path(path).read_bytes();i=b.find(b'\xff Go buildinf:')
    guard.require(i>=0 and b[i+15]&2,'Go build info not found')
    p=i+32;n=0;shift=0
    while True:
        x=b[p];p+=1;n|=(x&127)<<shift
        if x<128:break
        shift+=7
    return b[p:p+n].decode()

def save(name,value):guard.save(FOLDER/name,value)
def load(name):return guard.load(FOLDER/name)

def codex_settings_hash():
    # This release does not own codex2api's RPM. Snapshot current settings,
    # including the operator's later 80->60 change, rather than imposing history.
    data=guard.codex_api('/api/admin/settings')
    keys=['global_rpm','max_concurrency','response_cache_write_policy','response_cache_local_max_bytes',
      'response_cache_local_max_entry_bytes','response_cache_reconstruct_max_bytes','model_mapping',
      'codex_model_mapping','billing_tier_policy','codex_force_websocket','prompt_filter_enabled']
    return hashlib.sha256(guard.canonical({k:data.get(k) for k in keys})).hexdigest()
guard.codex_settings_hash=codex_settings_hash

def prepare():
    if (FOLDER/'before.json').exists():
        guard.require(not (FOLDER/'image.json').exists() and not (FOLDER/'state.json').exists(),'Already prepared or cut over')
        guard.require(guard.fingerprint(ops.inspect('new-api'))==guard.fingerprint(load('before.json')),'Partial prepare baseline changed')
    guard.require(guard.digest(FOLDER/'candidate.tar.gz')==ARCHIVE,'Artifact archive checksum mismatch')
    # Debian 12's Python 3.11.2 lacks tarfile's later data filter. Only extract
    # our exact signed-off regular root files, never links or arbitrary paths.
    allowed={'new-api','build-report.json','artifact-verification.json','buildinfo.txt','baseline-buildinfo.txt','frontend-manifest.json','build.py','README.md','source.tar.gz','SHA256SUMS'}
    with tarfile.open(FOLDER/'candidate.tar.gz') as t:
        members=t.getmembers()
        guard.require(len(members)==len(allowed) and {m.name for m in members}==allowed and all(m.isfile() for m in members),'Unexpected archive members')
        for member in members:
            target=FOLDER/member.name
            guard.require(not target.is_symlink(),'Unsafe extraction destination')
            with t.extractfile(member) as src,target.open('wb') as dst:shutil.copyfileobj(src,dst)
    for p in FOLDER.iterdir():
        if p.is_file():p.chmod(0o600)
    (FOLDER/'new-api').chmod(0o755);(ROOT/'tls-check').chmod(0o755)
    guard.require(guard.digest(FOLDER/'new-api')==BINARY and build_version(FOLDER/'new-api')=='go1.26.10','Candidate binary identity mismatch')
    current=ops.inspect('new-api')
    guard.require(current['State']['Running'] and current['Image']==OLD_IMAGE and guard.binary_sha(current)==OLD_BINARY,'Live baseline changed')
    guard.require(all(m['Type']=='bind' for m in current['Mounts']),'Unexpected non-bind mount')
    ops.run(['docker','cp',current['Id']+':/new-api',str(FOLDER/'previous-program')])
    guard.require(guard.digest(FOLDER/'previous-program')==OLD_BINARY,'Filesystem baseline differs from runtime')
    _,actual=recover(FOLDER/'new-api')
    expected=load('frontend-manifest.json')
    guard.require({k:hashlib.sha256(v).hexdigest() for k,v in actual.items()}==expected and len(actual)==956,'Embedded frontend mismatch')
    old_manifest=guard.load('/opt/carpool/releases/resource-guard-20261021/new-api/frontend-manifest.json')
    guard.require(expected==old_manifest.get('files',old_manifest),'Frontend differs from deployed baseline')
    save('before.json',current);save('invariants.json',guard.invariants());save('others.json',guard.other_containers('new-api'))
    save('build-manifest.json',{'service':'new-api','version':VERSION,'binary_sha256':BINARY,'baseline_binary_sha256':OLD_BINARY,'assets_unchanged':True,'assets_count':956,'go_version':'go1.26.10'})
    base='local/new-api-security-base:20261022'
    ops.run(['docker','tag',OLD_IMAGE,base])
    (FOLDER/'Dockerfile').write_text('FROM '+base+'\nCOPY new-api /new-api\n')
    (FOLDER/'.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker','build','--network=none','--pull=false','-t',IMAGE,str(FOLDER)])
    image=ops.docker('GET','/images/'+IMAGE+'/json')['Id']
    candidate=guard.expected('new-api',current,image,{})
    candidate['Labels'].update({'carpool.release':RELEASE,'carpool.binary.sha256':BINARY})
    guard.require(dict(x.split('=',1) for x in candidate['Env'])==ops.envmap(current),'Environment changed')
    save('candidate-spec.json',candidate);save('image.json',{'id':image,'tag':IMAGE,'binary':BINARY})
    print('Prepared candidate; old program/config retained; 956 frontend files verified',flush=True)

def stage():
    name='newapi-go12610-stage';guard.require(ops.maybe_inspect(name) is None,'Stage name occupied')
    network=ops.docker('GET','/networks/bridge');host=network['IPAM']['Config'][0]['Gateway']
    seen=[]
    class Mock(BaseHTTPRequestHandler):
        def log_message(self,*a):pass
        def do_POST(self):
            request=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            seen.append({'model':request.get('model'),'max_tokens':request.get('max_tokens'),'reasoning':request.get('reasoning')})
            raw=json.dumps({'id':'security-stage','object':'chat.completion','created':int(time.time()),'model':request['model'],
                'choices':[{'index':0,'message':{'role':'assistant','content':'OK'},'finish_reason':'stop'}],
                'usage':{'prompt_tokens':9,'completion_tokens':2,'total_tokens':11}}).encode()
            self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
    mock=ThreadingHTTPServer((host,0),Mock);threading.Thread(target=mock.serve_forever,daemon=True).start()
    image=load('image.json')['id']
    cfg={'Image':image,'Env':['GIN_MODE=release','SESSION_SECRET='+secrets.token_hex(32),'CRYPTO_SECRET='+secrets.token_hex(32),'BATCH_UPDATE_ENABLED=false','CARPOOL_USAGE_AGGREGATE_ENABLED=true'],
        'Labels':{guard.LABEL:RELEASE,'scope':'isolated-security-sqlite'},
        'HostConfig':{'NetworkMode':'bridge','Memory':536870912,'NanoCpus':1000000000,
          'Binds':[str(ROOT/'tls-check')+':/probe/tls-check:ro'],
          'PortBindings':{'3000/tcp':[{'HostIp':'127.0.0.1','HostPort':'17868'}]}},'ExposedPorts':{'3000/tcp':{}}}
    cid=ops.docker('POST','/containers/create?name='+name,cfg)['Id'];save('stage-container.json',{'id':cid})
    try:
        ops.docker('POST','/containers/'+cid+'/start');url='http://127.0.0.1:17868';ops.wait_http(url,'/api/status')
        tls=json.loads(ops.run(['docker','exec',cid,'/probe/tls-check']));guard.require(tls['passed'] and tls['go_version']=='go1.26.10','Candidate runtime TLS failed');save('tls-verification.json',tls)
        import urllib.request
        opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));uid=None
        def call(method,path,body=None):
            status,data=ops.api(url,method,path,body,user_id=uid,opener=opener)
            guard.require(status==200 and data.get('success') is True,'Stage API failed: '+path)
            return data.get('data')
        pw=secrets.token_urlsafe(24);call('POST','/api/setup',{'username':'sec_stage','password':pw,'confirmPassword':pw})
        uid=call('POST','/api/user/login',{'username':'sec_stage','password':pw})['id']
        call('PUT','/api/option/',{'key':'theme.frontend','value':'classic'})
        guard.require(call('GET','/api/status')['version']==VERSION,'Stage version mismatch')
        call('PUT','/api/option/',{'key':'ModelRatio','value':'{"deepseek/deepseek-v4.1-flash":0.1}'})
        for channel in [55,56]:
            call('POST','/api/channel/',{'mode':'single','channel':{'id':channel,'type':1,'status':1,'name':'mock-'+str(channel),'key':'mock','base_url':'http://'+host+':'+str(mock.server_port),'models':'deepseek/deepseek-v4.1-flash','group':'default'}})
            call('GET','/api/channel/test/'+str(channel)+'?model=deepseek%2Fdeepseek-v4.1-flash&stream=false')
        guard.require(len(seen)==2 and seen[0]['max_tokens']==256 and seen[0]['reasoning']=={'enabled':False} and seen[1]['max_tokens']==16,'Channel-test compatibility changed')
        aggregate=call('POST','/api/log/usage/aggregate',{'version':1,'tokens':[]})
        guard.require(aggregate['complete'] and aggregate['totals']['requests']==0,'Aggregate API failed')
        with urllib.request.urlopen(url+'/',timeout=10) as r:html=r.read().decode()
        guard.require('codex-routes-20261005' in html,'Current production frontend namespace missing')
        save('stage-results.json',{'passed':True,'image':image,'tls':True,'channel_requests':seen,'aggregate_empty':True,'frontend_namespace':True})
        print('Stage passed: valid TLS certificate chains, isolated Cline tests, aggregate API and frontend',flush=True)
    finally:
        mock.shutdown();mock.server_close()
        guard.require(ops.inspect(cid)['Config']['Labels'].get('scope')=='isolated-security-sqlite','Stage ownership changed')
        ops.docker('DELETE','/containers/'+cid+'?force=true&v=true')

original_verify=guard.verify
def verify(service='new-api'):
    original_verify(service)
    live=ops.inspect(service)
    guard.require(build_version('/proc/'+str(live['State']['Pid'])+'/exe')=='go1.26.10','Runtime Go toolchain is not patched')
    cp=ops.envmap(ops.inspect('carpool-backend'))
    headers={'Authorization':'Bearer '+cp['NEWAPI_ADMIN_TOKEN'],'New-Api-User':cp['NEWAPI_ADMIN_USER_ID']}
    log=guard.get_json('http://127.0.0.1:7862/api/log/?type=2&token_ids=1&page_size=1',headers)
    guard.require(log.get('success') and log.get('data',{}).get('token_filter_applied'),'Carpool scoped log integration failed')
    code,data=ops.api('http://127.0.0.1:7862','POST','/api/log/usage/aggregate',{'version':1,'tokens':[]},token=cp['NEWAPI_ADMIN_TOKEN'],user_id=cp['NEWAPI_ADMIN_USER_ID'])
    guard.require(code==200 and data.get('success') and data['data']['complete'],'Live aggregate API failed')
    save('security-verification.json',{'passed':True,'go_version':'go1.26.10','scoped_logs':True,'aggregate':True,'go_security_fix':'CVE-2026-111943','other_services_unchanged':True})
    print('Verified patched live runtime, scoped logs and aggregate integration',flush=True)
guard.verify=verify

def main():
    action=sys.argv[1]
    with pathlib.Path('/opt/carpool/releases/.newapi-software-release.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        if action=='prepare':return prepare()
        if action=='stage':return stage()
        if action=='cutover':
            guard.require(load('stage-results.json')['passed'],'Candidate not staged')
            return guard.cutover('new-api')
        if action=='verify':return verify()
        if action=='rollback':return guard.rollback('new-api')
        raise RuntimeError('Unknown action')

if __name__=='__main__':
    try:main()
    except Exception as e:
        print(json.dumps({'failed':True,'reason':str(e) if isinstance(e,RuntimeError) else type(e).__name__}));sys.exit(1)
