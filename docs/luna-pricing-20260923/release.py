"""Server-side isolated validation and minimal release, no credentials printed."""
import copy, hashlib, http.cookiejar, importlib.util, json, math, pathlib, re, secrets, subprocess, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = pathlib.Path('/opt/carpool/releases/luna-pricing-20260923')
spec = importlib.util.spec_from_file_location('base_release', '/opt/carpool/releases/sensitive-message-20260922/deploy.py')
base = importlib.util.module_from_spec(spec); spec.loader.exec_module(base)
ops = base.ops
VERSION = 'v0.13.2-carpool-r15.7-luna-20260923'
OLD = 'sha256:e0bce69b70a23f0d558c0f50fe4ac54c1bd514c6de10bc91fe062a3b99cac502'
IMAGE = 'hzqicoding/new-api:carpool-r15.7-luna-20260923'
ops.ROOT = base.ROOT = ROOT
ops.RELEASE = base.RELEASE = 'luna-pricing-20260923'
base.IMAGE, base.OLD, base.VERSION = IMAGE, OLD, VERSION
base.BINARY = hashlib.sha256((ROOT/'new-api').read_bytes()).hexdigest()
ALIASES = {
 '5.6': ['gpt-5.6-luna', 'databricks-gpt-5-6-luna', 'openai-gpt-56-luna', 'openai-gpt-56-luna-pro', 'openai.gpt-5.6-luna', 'openai/gpt-5.6-luna', 'openai/gpt-5.6-luna-pro'],
 '6': ['gpt-6-luna'],
}
EXPRS = {
 '5.6': 'len <= 272000 ? tier("0_272k", p * 0.26 + c * 1.56 + cr * 0.026 + cc * 0.325) : tier("272k_plus", p * 0.52 + c * 2.34 + cr * 0.052 + cc * 0.65)',
 '6': 'len <= 272000 ? tier("0_272k", p * 0.13 + c * 0.65 + cr * 0.013 + cc * 0.1625) : tier("272k_plus", p * 0.26 + c * 0.975 + cr * 0.026 + cc * 0.325)',
}
KEYS = ['billing_setting.billing_mode', 'billing_setting.billing_expr', 'billing_setting.group_ratio_override']

def mysql(sql):
    before = ops.load('production-before.json')['containers']['new-api']
    dsn = ops.envmap(before)['SQL_DSN']
    user, password, _, db = re.match(r'([^:]+):(.*?)@tcp\((.*?)\)/([^?]+)', dsn).groups()
    return ops.run(['docker','exec','-i','-e','MYSQL_PWD='+password,'1Panel-mysql-HbLv','mysql','-u'+user,'--default-character-set=utf8mb4','--batch','--raw','--skip-column-names',db], sql.encode()).decode()

def read_options():
    return {r['k']:r['v'] for r in (json.loads(l) for l in mysql('SELECT JSON_OBJECT("k",`key`,"v",value) FROM options;').splitlines())}

def desired(old):
    maps = {k:json.loads(old.get(k,'{}')) for k in KEYS}
    for family, models in ALIASES.items():
        for m in models:
            maps[KEYS[0]][m] = 'tiered_expr'
            maps[KEYS[1]][m] = EXPRS[family]
            maps[KEYS[2]][m] = 1
    return {k:json.dumps(v,ensure_ascii=False) for k,v in maps.items()}

def put_options(values):
    parts = ['START TRANSACTION;']
    for k,v in values.items():
        kh = k.encode().hex()
        if v is None: parts.append(f'DELETE FROM options WHERE `key`=CONVERT(0x{kh} USING utf8mb4);')
        else:
            vh=v.encode().hex()
            parts.append(f'INSERT INTO options (`key`,value) VALUES (CONVERT(0x{kh} USING utf8mb4),CONVERT(0x{vh} USING utf8mb4)) ON DUPLICATE KEY UPDATE value=VALUES(value);')
    mysql('\n'.join(parts+['COMMIT;']))

def prepare():
    current=ops.inspect('new-api'); assert current['Image']==OLD
    ops.save('production-before.json',{'containers':{'new-api':current}})
    ops.save('options-before.json',read_options())
    ops.save('options-after.json',desired(ops.load('options-before.json')))
    (ROOT/'new-api').chmod(0o755)
    ops.run(['docker','tag',OLD,'hzqicoding/new-api:luna-base-20260923'])
    (ROOT/'Dockerfile').write_text('FROM hzqicoding/new-api:luna-base-20260923\nCOPY new-api /new-api\n')
    (ROOT/'.dockerignore').write_text('*\n!Dockerfile\n!new-api\n')
    ops.run(['docker','build','--network=none','--pull=false','-t',IMAGE,str(ROOT)])
    image=json.loads(ops.run(['docker','image','inspect',IMAGE]))[0]['Id']
    ops.save('image.json',{'id':image,'tag':IMAGE,'binary':base.BINARY})
    print('Image prepared; production unchanged',flush=True)

def stage():
    class Mock(BaseHTTPRequestHandler):
        def log_message(self,*args): pass
        def do_POST(self):
            b=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            long=b['messages'][0]['content']=='long'
            prompt, cached=(272001,262001) if long else (100000,90000)
            out={'id':'stage-mock','object':'chat.completion','created':int(time.time()),'model':b['model'],'choices':[{'index':0,'message':{'role':'assistant','content':'OK'},'finish_reason':'stop'}], 'usage':{'prompt_tokens':prompt,'completion_tokens':1000,'total_tokens':prompt+1000,'prompt_tokens_details':{'cached_tokens':cached}}}
            raw=json.dumps(out).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
    mock=ThreadingHTTPServer(('172.19.0.1',0),Mock)
    threading.Thread(target=mock.serve_forever,daemon=True).start()
    name='newapi-luna-stage-20260923'; assert ops.maybe_inspect(name) is None
    image=ops.load('image.json')['id']
    cfg={'Image':image,'Env':['GIN_MODE=release','SESSION_SECRET='+secrets.token_hex(32),'CRYPTO_SECRET='+secrets.token_hex(32),'BATCH_UPDATE_ENABLED=false'], 'Labels':{ops.LABEL:ops.RELEASE,'scope':'isolated-luna-sqlite'},'HostConfig':{'NetworkMode':'1panel-network','Memory':536870912,'NanoCpus':1000000000,'PortBindings':{'3000/tcp':[{'HostIp':'127.0.0.1','HostPort':'17864'}]}},'ExposedPorts':{'3000/tcp':{}}}
    cid=ops.docker('POST','/containers/create?name='+name,cfg)['Id']
    results=[]
    try:
        ops.docker('POST','/containers/'+cid+'/start');baseurl='http://127.0.0.1:17864';ops.wait_http(baseurl,'/api/status')
        opener=base.urllib.request.build_opener(base.urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));uid=None
        def call(method,path,body=None):
            status,d=ops.api(baseurl,method,path,body,user_id=uid,opener=opener)
            assert status==200 and d.get('success') is True,(path,status,d)
            return d.get('data')
        pw=secrets.token_urlsafe(24);call('POST','/api/setup',{'username':'luna_stage','password':pw,'confirmPassword':pw})
        uid=call('POST','/api/user/login',{'username':'luna_stage','password':pw})['id']
        for k,v in desired({}).items():call('PUT','/api/option/',{'key':k,'value':v})
        call('PUT','/api/option/',{'key':'GroupRatio','value':'{"default":1,"plus":0.32,"daycard":0.2,"free":0}'})
        call('PUT','/api/option/',{'key':'UserUsableGroups','value':'{"default":"default","plus":"plus","daycard":"daycard","free":"free"}'})
        for group in ['default','plus','daycard','free']:
            call('POST','/api/channel/',{'mode':'single','channel':{'type':1,'name':'mock-'+group,'key':'mock','base_url':'http://172.19.0.1:'+str(mock.server_port),'models':'gpt-5.6-luna,gpt-6-luna','group':group,'status':1}})
            call('POST','/api/token/',{'name':'stage-'+group,'expired_time':-1,'remain_quota':10000000,'unlimited_quota':True,'group':group})
        tokens=call('GET','/api/token/?p=1&page_size=20')['items']
        for token in tokens:
            key=call('POST','/api/token/'+str(token['id'])+'/key')['key']
            for family in ['5.6','6']:
                for length in ['short','long']:
                    model='gpt-'+family+'-luna'
                    status,d=ops.api(baseurl,'POST','/v1/chat/completions',{'model':model,'messages':[{'role':'user','content':length}],'max_tokens':1200,'stream':False},token=key)
                    assert status==200 and d.get('choices',[{}])[0].get('message',{}).get('content')=='OK',(status,d)
                    results.append({'group':token['group'],'model':model,'length':length,'status':status})
        # Read isolated instance logs to validate settled charges; no production account is used.
        logs=[]
        for attempt in range(10):
            logs=call('GET','/api/log/?type=2&p=1&page_size=100')['items']
            if len(logs)==16:break
            time.sleep(0.3)
        assert len(logs)==16,('settlement log count',len(logs))
        rows=[]
        for row in logs:
            model,group,prompt,quota,other=(row[k] for k in ['model_name','group','prompt_tokens','quota','other'])
            want={('gpt-5.6-luna',100000):3250,('gpt-5.6-luna',272001):10582,('gpt-6-luna',100000):1560,('gpt-6-luna',272001):5194}[(model,prompt)]
            assert quota==want,(model,group,prompt,quota,want)
            assert json.loads(other)['group_ratio']==1
            rows.append({'model':model,'group':group,'quota':quota,'prompt_tokens':prompt})
        pricing=ops.api(baseurl,'GET','/api/pricing')[1]['data']
        assert all(p.get('group_ratio_override')==1 for p in pricing if 'luna' in p['model_name'])
        ops.save('stage-results.json',{'passed':True,'image':image,'cases':results,'settlement_checks':len(rows)})
        print('Isolated relay and settlement passed: 16 requests, 4 groups, 2 models, both context tiers',flush=True)
    finally:
        mock.shutdown();mock.server_close()
        assert ops.inspect(cid)['Config']['Labels'].get('scope')=='isolated-luna-sqlite'
        ops.docker('DELETE','/containers/'+cid+'?force=true')

def restore_options():
    old=ops.load('options-before.json');put_options({k:old.get(k) for k in KEYS})

def cutover():
    before=ops.load('production-before.json')['containers']['new-api'];assert ops.inspect('new-api')['Id']==before['Id']
    # Preserve latest independent option edits; only change Luna entries in these maps.
    latest=read_options();ops.save('options-before.json',latest);ops.save('options-after.json',desired(latest))
    original_docker=ops.docker
    def docker(method,path,body=None,accepted=(200,201,204,304)):
        result=original_docker(method,path,body,accepted)
        if method=='POST' and path=='/containers/'+before['Id']+'/stop?t=30':put_options(ops.load('options-after.json'))
        return result
    ops.docker=docker
    try:base.cutover()
    except Exception:
        restore_options()
        raise
    finally:ops.docker=original_docker
    after=read_options();before_opts=ops.load('options-before.json')
    assert all(after.get(k)==v for k,v in before_opts.items() if k not in KEYS)
    pricing=ops.api('http://127.0.0.1:7862','GET','/api/pricing')[1]['data']
    lunar=[p for p in pricing if p['model_name'] in sum(ALIASES.values(),[])]
    assert lunar and all(p.get('group_ratio_override')==1 for p in lunar)
    ops.save('final-verification.json',{'passed':True,'luna_models':[p['model_name'] for p in lunar],'other_options_unchanged':True})
    print('Luna overrides active; unrelated options unchanged',flush=True)

def rollback():
    restore_options();ops.rollback('new-api')

if __name__=='__main__':
    {'prepare':prepare,'stage':stage,'cutover':cutover,'verify':base.verify_live,'rollback':rollback}[sys.argv[1]]()
