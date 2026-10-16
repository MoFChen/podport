#!/usr/bin/env python3
"""Optional Podman acceptance test; Python is not needed to run PodPort."""
import argparse,json,shutil,sqlite3,subprocess,tempfile,time,urllib.request,urllib.error,uuid
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--engine',default='podman',choices=['podman','docker']);p.add_argument('--image',default='localhost/podport:3.0.0');p.add_argument('--skip-build',action='store_true');a=p.parse_args()
if not shutil.which(a.engine):raise SystemExit(a.engine+' 不可用，请在目标容器主机运行。')
name='podport-qa-'+uuid.uuid4().hex[:10];volume=name+'-data';base='';passed=[]
def cmd(*args,capture=True):
 r=subprocess.run([a.engine,*args],check=True,capture_output=capture,text=True);return r.stdout.strip() if capture else ''
def check(label,value):
 if not value:raise AssertionError(label)
 passed.append(label);print('PASS',label,flush=True)
def request(path,body=None,method=None,expected=200):
 r=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),method=method or('POST' if body is not None else'GET'),headers={'Content-Type':'application/json'})
 try:
  with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(r,timeout=10) as response:code,raw=response.status,response.read()
 except urllib.error.HTTPError as e:code,raw=e.code,e.read()
 if code!=expected:raise AssertionError((path,code,raw[:300]))
 try:return json.loads(raw)
 except ValueError:return raw

def start():
 global base
 cmd('run','-d','--name',name,'-p','127.0.0.1::8080','-v',volume+':/data:Z','--memory=64m','--memory-swap=64m','-e','GOMEMLIMIT=48MiB','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges',a.image)
 port=cmd('port',name,'8080/tcp').splitlines()[0].rsplit(':',1)[1];base='http://127.0.0.1:'+port
 for _ in range(40):
  try:
   if request('/healthz')['status']=='ok':return
  except Exception:pass
  time.sleep(.5)
 raise AssertionError('容器未健康启动\n'+cmd('logs',name))
try:
 if not a.skip_build:cmd('build','-t',a.image,'.',capture=False)
 image=json.loads(cmd('image','inspect',a.image))[0];check('默认非 root 用户',image['Config'].get('User')=='65532:65532')
 print('Image bytes:',image.get('Size'))
 cmd('volume','create',volume);start();check('64 MiB 限制下健康启动',request('/healthz')['status']=='ok')
 container=json.loads(cmd('inspect',name))[0];check('容器内存硬限制为 64 MiB',container['HostConfig']['Memory']==64*1024*1024)
 cmd('exec',name,'/podport','-healthcheck');check('镜像内健康检查命令',True)
 check('内嵌网页',b'PodPort' in request('/'));check('正式版 API 配置',b"mode:'api'" in request('/config.js'))
 def listener(port,protocol='tcp',outer=0):
  return {'port_start':port,'protocol':protocol,'publications':[{'host_start':outer,'protocol':protocol}] if outer else []}
 e={'host':'qa','pod':'web','container':'nginx','scope':'pod','networks':['media-net'],'listeners':[listener(80,outer=8080),listener(443,'both',8443)]}
 saved=request('/api/entries',e,expected=201);check('多个监听与双协议保存',len(saved['listeners'])==2 and saved['listeners'][1]['protocol']=='both')
 request('/api/entries',{**e,'container':'two','listeners':[listener(80,outer=8081)]},expected=409);check('Pod 内部冲突',len(request('/api/entries')['entries'])==1)
 request('/api/entries',{'host':'qa','container':'other','listeners':[listener(90,'udp',8443)]},expected=409);check('双协议主机映射占用 UDP',len(request('/api/entries')['entries'])==1)
 emby={'host':'qa','container':'emby','networks':['media-net'],'listeners':[listener(8096),listener(8920)]};request('/api/entries',emby,expected=201);check('Pod 与独立容器加入同一自定义网络',len(request('/api/entries')['entries'])==2)
 raw=request('/api/backup')
 with tempfile.TemporaryDirectory() as temp:
  path=Path(temp)/'snapshot.db';path.write_bytes(raw)
  with sqlite3.connect(path) as db:check('快照完整性',db.execute('PRAGMA integrity_check').fetchone()[0]=='ok')
 before=request('/api/entries')['entries'];cmd('rm','-f',name);start();check('重建容器后数据保留',request('/api/entries')['entries']==before)
 check('持久化卷可写',request('/api/entries',{'host':'qa','container':'after-restart','listeners':[listener(1234,outer=8181)]},expected=201)['listeners'][0]['publications'][0]['host_start']==8181)
 print(json.dumps({'status':'passed','checks':len(passed),'memory_limit_bytes':64*1024*1024},ensure_ascii=False))
finally:
 subprocess.run([a.engine,'rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run([a.engine,'volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
