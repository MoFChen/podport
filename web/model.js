export const labels = {active:'使用中', reserved:'预留', released:'已释放'};
export const range = (a,b=a) => a===b ? String(a) : `${a}–${b}`;
export const protocolLabel = p => p==='both'?'TCP + UDP':p.toUpperCase();
export const protocols = p => p==='both'?['tcp','udp']:[p];
export const blank = () => ({id:'',host:'本机',pod:'',container:'',scope:'auto',networks:[],listeners:[],note:'',updated_at:''});
export const blankListener = () => ({id:crypto.randomUUID(),service:'',listen_ip:'0.0.0.0',port_start:0,port_end:0,protocol:'tcp',status:'active',note:'',publications:[]});
export const blankPublication = protocol => ({host_ip:'0.0.0.0',host_start:0,host_end:0,protocol:protocol||'tcp',status:'active'});
export function parseRange(value,optional=false) {
 const v=String(value).trim();if(optional&&!v)return[0,0];
 if(!/^\d{1,5}(?:\s*[-–]\s*\d{1,5})?$/.test(v))throw Error('请输入端口或端口范围，例如 8080、8000-8010');
 const[a,b=a]=v.split(/\s*[-–]\s*/).map(Number);if(a<1||b<a||b>65535)throw Error('端口范围必须在 1–65535 内');return[a,b];
}
const checkName=v=>v.length<=100&&!/[\u0000-\u001f\u007f-\u009f]/.test(v);
const ip=v=>/^(0|[1-9]\d{0,2})(\.(0|[1-9]\d{0,2})){3}$/.test(v)&&v.split('.').every(x=>+x<=255);
export function normalize(input) {
 const e={...blank(),...input};for(const k of['host','pod','container','note'])e[k]=e[k].trim();e.host||='本机';
 if(!e.container)throw Error('请填写容器名称');if(![e.host,e.pod,e.container].every(checkName)||e.note.length>1000)throw Error('名称或备注无效');
 if(!e.scope||e.scope==='auto')e.scope=e.pod?'pod':'container';
 if(!['pod','container','host'].includes(e.scope)||e.scope==='pod'&&!e.pod)throw Error('网络模式与 Pod 不匹配');
 e.networks=[...new Set((e.networks||[]).map(n=>n.trim()).filter(Boolean))].sort();if(e.networks.length>32||!e.networks.every(n=>checkName(n)&&!/[，,;；]/.test(n)))throw Error('网络名称无效或超过 32 个');
 if(e.scope==='host'&&e.networks.length)throw Error('host 网络不能同时加入自定义网络');
 if(e.listeners.length>128)throw Error('每个容器最多登记 128 个监听端口');const ids=new Set();
 e.listeners=e.listeners.map(input=>{
  const l={...blankListener(),...input};l.id||=crypto.randomUUID();if(l.id.length>120||ids.has(l.id))throw Error('监听 ID 重复或无效');ids.add(l.id);
  l.service=l.service.trim();l.note=l.note.trim();if(!checkName(l.service)||l.note.length>1000)throw Error('端口名称或备注过长');l.listen_ip||='0.0.0.0';
  if(!ip(l.listen_ip))throw Error('请输入明确的 IPv4 监听地址');[l.port_start,l.port_end]=parseRange(range(l.port_start,l.port_end||l.port_start));
  l.protocol||='tcp';l.status||='active';if(!['tcp','udp','both'].includes(l.protocol)||!labels[l.status])throw Error('协议或状态无效');
  if(l.publications.length>64)throw Error('一个监听最多登记 64 个主机映射');
  l.publications=l.publications.map(input=>{
   const p={...blankPublication(l.protocol),...input};if(e.scope==='host')throw Error('host 网络无须填写对外映射');p.host_ip||='0.0.0.0';if(!ip(p.host_ip))throw Error('请输入明确的 IPv4 主机绑定地址');
   [p.host_start,p.host_end]=parseRange(range(p.host_start,p.host_end||p.host_start));if(p.host_end-p.host_start!==l.port_end-l.port_start)throw Error('主机与内部端口范围长度必须一致');
   p.protocol||=l.protocol;p.status||='active';if(!['tcp','udp','both'].includes(p.protocol)||!protocols(p.protocol).every(x=>protocols(l.protocol).includes(x))||!labels[p.status])throw Error('映射协议必须包含在内部监听协议中，状态须有效');return p;
  });return l;
 });return e;
}
export function claims(e) {
 const host=e.host.toLowerCase(),namespace=JSON.stringify(e.scope==='pod'?['pod',host,e.pod]:e.scope==='host'?['host',host]:['container',host,e.container]);const out=[];
 for(const l of e.listeners){if(l.status==='released')continue;for(const protocol of protocols(l.protocol))out.push({namespace,ip:l.listen_ip,start:l.port_start,end:l.port_end,kind:e.scope,protocol,service:l.service});
  for(const p of l.publications){if(p.status==='released')continue;for(const protocol of protocols(p.protocol))out.push({namespace:JSON.stringify(['host',host]),ip:p.host_ip,start:p.host_start,end:p.host_end,kind:'host',protocol,service:l.service});}}
 return out;
}
const overlap=(a,b)=>a.namespace===b.namespace&&a.protocol===b.protocol&&a.start<=b.end&&b.start<=a.end&&(a.ip===b.ip||a.ip==='0.0.0.0'||b.ip==='0.0.0.0');
export function conflicts(a,entries) {
 const out=[],ac=claims(a),add=(x,y,b)=>out.push({kind:x.kind,with_id:b.id,with_label:[b.host,b.pod,b.container,y.service].filter(Boolean).join(' / '),port_start:Math.max(x.start,y.start),port_end:Math.min(x.end,y.end),protocol:x.protocol});
 for(let i=0;i<ac.length;i++)for(let j=0;j<i;j++)if(overlap(ac[i],ac[j])){add(ac[i],ac[j],a);if(out.length>=20)return out;}
 for(const b of entries){if(a.id&&a.id===b.id)continue;const bc=claims(b);for(const x of ac)for(const y of bc)if(overlap(x,y)){add(x,y,b);if(out.length>=20)return out;}}
 return out;
}
export function validateEntries(entries) {
 if(entries.length>5000)throw Error('最多登记 5000 个容器');const out=[],ids=new Set(),names=new Set(),pods=new Map();let count=0;
 for(const input of entries){const e=normalize(input);e.id||=crypto.randomUUID();if(e.id.length>120||ids.has(e.id))throw Error('容器 ID 重复或无效');ids.add(e.id);const key=e.host.toLowerCase()+'\0'+e.container;
  if(names.has(key))throw Error('同一主机上的容器名称必须唯一，请编辑已有容器');names.add(key);if(e.scope==='pod'){const k=e.host.toLowerCase()+'\0'+e.pod,n=JSON.stringify(e.networks);if(pods.has(k)&&pods.get(k)!==n)throw Error('同一 Pod 的自定义网络必须一致');pods.set(k,n);}
  count+=e.listeners.reduce((n,l)=>n+1+l.publications.length,0);if(count>20000)throw Error('最多登记 20000 个监听和映射');const c=conflicts(e,out);if(c.length){const err=Error('端口与已有登记冲突，请调整端口或绑定地址');err.conflicts=c;throw err;}out.push(e);
 }return out;
}
export function mergeEntry(e,all){return validateEntries(all.filter(v=>v.id!==e.id).map(v=>v.scope==='pod'&&e.scope==='pod'&&v.host.toLowerCase()===e.host.toLowerCase()&&v.pod===e.pod?{...v,networks:e.networks}:v).concat(e));}
export function matches(e,q,host,status,network='') {
 if(host&&e.host!==host||network&&!e.networks.includes(network)||status&&!e.listeners.some(l=>l.status===status))return false;q=q.trim().toLowerCase();if(!q)return true;
 if(/^\d+$/.test(q))return e.listeners.some(l=>l.port_start<=+q&&+q<=l.port_end||l.publications.some(p=>p.host_start<=+q&&+q<=p.host_end));
 return[e.host,e.pod,e.container,e.note,...e.networks,...e.listeners.flatMap(l=>[l.service,l.note,l.listen_ip,protocolLabel(l.protocol),...l.publications.flatMap(p=>[p.host_ip,protocolLabel(p.protocol)])])].join(' ').toLowerCase().includes(q);
}
export function reachable(a,b,l){if(a.host.toLowerCase()!==b.host.toLowerCase()||l.status==='released')return false;if(a.scope==='pod'&&b.scope==='pod'&&a.pod===b.pod)return true;return !l.listen_ip.startsWith('127.')&&a.networks.some(n=>b.networks.includes(n));}
export function demoEntries(){
 const listener=(service,port,protocol='tcp',outer=0)=>({...blankListener(),service,port_start:port,port_end:port,protocol,publications:outer?[{...blankPublication(protocol),host_start:outer,host_end:outer}]:[]});
 return [
 {...blank(),id:'nginx',host:'nas',pod:'web',container:'nginx',scope:'pod',networks:['media-net'],listeners:[listener('HTTP 反向代理',80,'tcp',8080),listener('HTTPS / HTTP3',443,'both',8443)]},
 {...blank(),id:'api',host:'nas',pod:'web',container:'api',scope:'pod',networks:['media-net'],listeners:[listener('应用接口',3000),listener('运行指标',9090)]},
 {...blank(),id:'emby',host:'nas',container:'emby',scope:'container',networks:['media-net'],note:'nginx 可通过同一网络访问 emby:8096',listeners:[listener('媒体服务',8096),listener('HTTPS',8920)]},
 {...blank(),id:'sync',host:'nas',container:'syncthing',scope:'container',networks:['sync-net'],listeners:[listener('同步',22000,'both',22000),listener('发现',21027,'udp',21027),listener('管理面板',8384,'tcp',8384)]}
 ].map(normalize);
}
export function validateImport(data){
 if(data.schema_version===3&&Array.isArray(data.entries))return validateEntries(data.entries);
 if(data.schema_version===2&&Array.isArray(data.entries)){
  const groups=new Map();for(const r of data.entries){const k=r.host.toLowerCase()+'\0'+r.container;let e=groups.get(k);if(!e){e={...blank(),id:r.id,host:r.host,pod:r.pod,container:r.container,scope:r.scope};groups.set(k,e);}if(e.pod!==r.pod||e.scope!==r.scope)throw Error('旧数据的同名容器网络不一致');let l=e.listeners.find(l=>l.listen_ip===r.listen_ip&&l.port_start===r.port_start&&l.port_end===(r.port_end||r.port_start)&&l.protocol===r.protocol);if(!l){l={...blankListener(),id:r.id,service:r.service||'',listen_ip:r.listen_ip,port_start:r.port_start,port_end:r.port_end||r.port_start,protocol:r.protocol,status:r.status,note:r.note||''};e.listeners.push(l);}if(r.status==='active'||r.status==='reserved'&&l.status==='released')l.status=r.status;if(r.host_start)l.publications.push({host_ip:r.host_ip,host_start:r.host_start,host_end:r.host_end||r.host_start,protocol:r.protocol,status:r.status});}return validateEntries([...groups.values()]);
 }throw Error('演示支持 v2/v3 JSON；Go 版也支持 v1 数据');
}
