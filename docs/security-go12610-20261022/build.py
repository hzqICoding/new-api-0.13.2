"""Isolated LAN build; no production configuration or credentials are required."""
import datetime, hashlib, json, os, pathlib, subprocess, tarfile, time

ROOT = pathlib.Path('/home/uatair/builds/newapi-go12610-20261022')
TOOLCHAIN = pathlib.Path('/home/uatair/.local/toolchains/go1.26.10')
VERSION = 'v0.13.2-carpool-r15.20-go12610-20261022'
GO_SHA = 'ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5'
SOURCE_SHA = '3b5072f987d7b1830a2dac970ffc5b6662e1eba943c72409ef6ee2b7958d626a'

def digest(p): return hashlib.sha256(p.read_bytes()).hexdigest()

def memory_available():
    for line in pathlib.Path('/proc/meminfo').read_text().splitlines():
        if line.startswith('MemAvailable:'):return int(line.split()[1])

def tree_rss(parent):
    children={};rss={}
    for path in pathlib.Path('/proc').iterdir():
        if not path.name.isdigit():continue
        try:
            fields={line.split(':',1)[0]:line.split(':',1)[1].strip() for line in (path/'status').read_text().splitlines() if ':' in line}
            pid=int(path.name);ppid=int(fields['PPid']);children.setdefault(ppid,[]).append(pid)
            rss[pid]=int(fields.get('VmRSS','0 kB').split()[0])
        except (FileNotFoundError,ProcessLookupError,PermissionError,KeyError):continue
    todo=[parent];seen=set()
    while todo:
        p=todo.pop()
        if p in seen:continue
        seen.add(p);todo.extend(children.get(p,[]))
    return sum(rss.get(p,0) for p in seen)

assert ROOT.resolve()==ROOT and os.getuid()!=0
assert digest(ROOT/'go1.26.10.linux-amd64.tar.gz')==GO_SHA
assert digest(ROOT/'source.tar.gz')==SOURCE_SHA
if not TOOLCHAIN.exists():
    TOOLCHAIN.mkdir(parents=True,mode=0o700)
    with tarfile.open(ROOT/'go1.26.10.linux-amd64.tar.gz') as archive:archive.extractall(TOOLCHAIN,filter='data')
GO=TOOLCHAIN/'go/bin/go'
SRC=ROOT/'src'
if not SRC.exists():
    SRC.mkdir(mode=0o700)
    with tarfile.open(ROOT/'source.tar.gz') as archive:archive.extractall(SRC,filter='data')
(ROOT/'out').mkdir(exist_ok=True)
(ROOT/'logs').mkdir(exist_ok=True)
env=os.environ.copy()
env.update({'PATH':str(GO.parent)+':'+env['PATH'],'GOENV':'off','GOTOOLCHAIN':'local',
  'GOPROXY':'https://goproxy.cn|https://proxy.golang.org|direct','GOSUMDB':'sum.golang.org',
  'GOCACHE':'/home/uatair/.cache/newapi-go12610','GOMODCACHE':'/home/uatair/.cache/newapi-gomod',
  'GOMAXPROCS':'6','GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'})
go_version=subprocess.check_output([str(GO),'version'],env=env,text=True).strip()
assert go_version=='go version go1.26.10 linux/amd64',go_version
before={str(p.relative_to(SRC)):digest(p) for p in SRC.rglob('*') if p.is_file() and (p.suffix=='.go' or p.name in ['go.mod','go.sum'])}
frontend={str(p.relative_to(SRC)):digest(p) for path in ['web/classic/dist','web/default/dist'] for p in (SRC/path).rglob('*') if p.is_file()}
expected=json.loads((ROOT/'frontend-manifest.json').read_text())
assert frontend==expected and len(frontend)==956
report={'host':'lan205','version':VERSION,'go_version':go_version,'source_sha256':SOURCE_SHA,
 'source_commit':'cc8369c39717d5b76506404d23f74a21df68e038','source_count':len(before),
 'frontend_count':len(frontend),'parallel_packages':6,'GOMAXPROCS':6,'steps':[],
 'started_at':datetime.datetime.now().astimezone().isoformat()}
print('Verified Go toolchain, production source and 956 frontend resources',flush=True)

def run_step(name,cmd):
    start=time.monotonic();next_progress=start+25;peak=0;minimum=memory_available()
    with (ROOT/'logs'/f'{name}.log').open('w') as log:
        process=subprocess.Popen(cmd,cwd=SRC,env=env,stdout=log,stderr=subprocess.STDOUT)
        while True:
            peak=max(peak,tree_rss(process.pid));minimum=min(minimum,memory_available())
            code=process.poll()
            if code is not None:break
            now=time.monotonic()
            if now>=next_progress:
                print(f'{name}: {now-start:.0f}s elapsed, sampled process-tree RSS peak {peak/1024:.0f}MiB, host available >= {minimum/1024:.0f}MiB',flush=True)
                next_progress=now+25
            time.sleep(0.8)
    step={'name':name,'seconds':round(time.monotonic()-start,2),'exit_code':code,'sampled_tree_rss_peak_mib':round(peak/1024,1),'minimum_host_available_mib':round(minimum/1024,1)}
    report['steps'].append(step);(ROOT/'build-report.json').write_text(json.dumps(report,indent=2))
    print(json.dumps(step),flush=True)
    if code:
        print((ROOT/'logs'/f'{name}.log').read_text(errors='replace')[-5000:],flush=True)
        raise SystemExit(code)

run_step('dependencies',[str(GO),'mod','download'])
run_step('build',[str(GO),'build','-p','6','-mod=readonly','-trimpath','-ldflags','-s -w -X github.com/QuantumNous/new-api/common.Version='+VERSION,'-o',str(ROOT/'out/new-api'),'.'])
run_step('x509',[str(GO),'test','-p','6','crypto/x509','-count=1'])
run_step('regressions',[str(GO),'test','-p','6','-mod=readonly','./controller','./relay/helper','./relay/channel','./setting/billing_setting','./service','./middleware','./pkg/billingexpr','-run','TestClineChannelTestParameters|TestRelaySensitiveWords|TestLunaGroupOverride|TestCarpool|TestCodexTrace|TestComputeTieredQuota','-count=1'])
after={str(p.relative_to(SRC)):digest(p) for p in SRC.rglob('*') if p.is_file() and (p.suffix=='.go' or p.name in ['go.mod','go.sum'])}
assert before==after,'Source or dependencies changed during build'
assert frontend=={str(p.relative_to(SRC)):digest(p) for path in ['web/classic/dist','web/default/dist'] for p in (SRC/path).rglob('*') if p.is_file()}
binary=ROOT/'out/new-api'
assert subprocess.check_output([str(binary),'--version'],cwd=ROOT,env=env,text=True).strip()==VERSION
buildinfo=subprocess.check_output([str(GO),'version','-m',str(binary)],env=env,text=True)
assert 'go1.26.10' in buildinfo
(ROOT/'buildinfo.txt').write_text(buildinfo)
report.update({'binary_sha256':digest(binary),'binary_bytes':binary.stat().st_size,'source_unchanged':True,'frontend_unchanged':True,'finished_at':datetime.datetime.now().astimezone().isoformat(),'passed':True})
(ROOT/'build-report.json').write_text(json.dumps(report,indent=2))
print('BUILD AND TESTS PASSED '+report['binary_sha256'],flush=True)
