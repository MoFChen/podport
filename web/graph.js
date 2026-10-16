import{range,protocolLabel}from'./model.js';
const NS='http://www.w3.org/2000/svg';
export const graphCSS=`
.g-edge{fill:none;stroke-width:2;stroke-dasharray:6 7;animation:flow 28s linear infinite}
.g-edge.network{stroke:var(--network);stroke-dasharray:none;opacity:.6}
.g-node{cursor:pointer;outline:none}.g-node:hover>.node-box,.g-node:focus>.node-box{stroke:var(--blue);stroke-width:2.5}
.g-node.conflict>.node-box{stroke:var(--red);stroke-width:2.5}.g-node.draft>.node-box{stroke:var(--blue);stroke-dasharray:6 4}
@keyframes flow{to{stroke-dashoffset:-260}}@media(prefers-reduced-motion:reduce){.g-edge{animation:none}}
`;
const svg=(tag,attrs={},value)=>{const n=document.createElementNS(NS,tag);for(const[k,v]of Object.entries(attrs))n.setAttribute(k,String(v));if(value!==undefined)n.textContent=String(value);return n;};
const text=(x,y,value,size=18,color='var(--text)',weight=400)=>svg('text',{x,y,fill:color,'font-size':size,'font-family':'system-ui,-apple-system,"Microsoft YaHei",sans-serif','font-weight':weight},value);
const short=(s,n=30)=>s.length>n?s.slice(0,n-1)+'…':s;
const paths={
 network:'M12 3v6M5 21v-6h14v6M12 15v-3M8 3h8v5H8zM2 18h6v4H2zM16 18h6v4h-6z',
 pod:'M3 6l9-4 9 4v12l-9 4-9-4zM3 6l9 5 9-5M12 11v11',
 container:'M3 5h18v14H3zM7 9v6M12 9v6M17 9v6',
 port:'M8 3v5M16 3v5M5 8h14v5a7 7 0 0 1-14 0zM12 20v3',
 host:'M3 3h18v7H3zM3 14h18v7H3zM6 6h.1M6 17h.1M10 6h8M10 17h8'
};
function icon(type,x,y,color){const n=svg('svg',{x,y,width:26,height:26,viewBox:'0 0 24 24',fill:'none',stroke:color,'stroke-width':1.6,'stroke-linecap':'round','stroke-linejoin':'round',class:'node-icon','aria-hidden':'true'});n.append(svg('path',{d:paths[type]||paths.container}));return n;}
function node(x,y,w,h,type,title,subtitle,options={}){
 const n=svg('g',{transform:`translate(${x} ${y})`,class:'g-node '+(options.cls||''),tabindex:0,role:'button','aria-label':options.label||title+' · '+subtitle});
 n.append(svg('rect',{width:w,height:h,rx:12,fill:'var(--surface)',stroke:'var(--border)',class:'node-box'}));
 n.append(svg('rect',{x:12,y:(h-40)/2,width:40,height:40,rx:10,fill:'var(--tint)',stroke:'none'}),icon(type,19,(h-26)/2,options.color||'var(--blue)'));
 n.append(text(65,h/2-5,short(title,options.max||28),18,'var(--text)',560),text(65,h/2+21,short(subtitle,options.subMax||34),14,'var(--muted)'));
 n.append(svg('title',{},options.label||title+' · '+subtitle));n.addEventListener('click',()=>options.onClick?.());n.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();options.onClick?.();}});return n;
}
const curve=(x,y,a,b)=>`M${x} ${y} C${(x+a)/2} ${y} ${(x+a)/2} ${b} ${a} ${b}`;
const line=(root,x,y,a,b,color,network=false)=>root.append(svg('path',{d:curve(x,y,a,b),stroke:color,class:'g-edge'+(network?' network':''),opacity:.75}));
export function renderGraph(canvas,entries,{conflictIDs=new Set(),onEdit,onHost,onNetwork}={}){
 canvas.replaceChildren();if(!entries.length){const empty=document.createElement('div');empty.className='empty';empty.textContent='没有匹配的登记，调整筛选条件后再看。';canvas.append(empty);return null;}
 let y=60;const hosts=[];
 for(const host of[...new Set(entries.map(e=>e.host))]){
  const top=y,all=entries.filter(e=>e.host===host),groups=[];y+=82;
  for(const key of[...new Set(all.map(e=>e.scope==='pod'?'pod:'+e.pod:'container:'+e.id))]){
   const members=all.filter(e=>(e.scope==='pod'?'pod:'+e.pod:'container:'+e.id)===key),groupTop=y;const isPod=members[0].scope==='pod';if(isPod)y+=50;
   const items=[];for(const e of members){const itemTop=y;const ports=[];for(const l of e.listeners){const h=Math.max(82,l.publications.length*68);ports.push({listener:l,y:y+h/2,height:h});y+=h+12;}
    if(!ports.length)y+=86;items.push({entry:e,top:itemTop,center:(itemTop+y-12)/2,ports});y+=20;}
   groups.push({members,items,top:groupTop,height:y-groupTop,center:(groupTop+y)/2,isPod,networks:members[0].networks});y+=28;
  }
  const networks=[...new Set(all.flatMap(e=>e.networks))];y=Math.max(y,top+110+networks.length*106);hosts.push({name:host,top,height:y-top,groups,networks});y+=54;
 }
 const height=Math.max(400,y),root=svg('svg',{viewBox:`0 0 1530 ${height}`,xmlns:NS,role:'img','aria-label':'自定义网络、Pod 与容器的内部监听及主机发布拓扑'}),world=svg('g',{class:'graph-world'});root.append(svg('title',{},'PodPort 网络与端口登记拓扑'),world);
 for(const h of hosts){
  world.append(svg('rect',{x:12,y:h.top,width:1506,height:h.height,rx:20,fill:'var(--canvas)',stroke:'var(--border)'}));
  const hostTitle=node(32,h.top+14,270,60,'host',h.name,'主机 · 手动登记',{onClick:()=>onHost?.(h.name)});world.append(hostTitle);
  world.append(text(360,h.top+51,'Pod / 容器',16,'var(--muted)'),text(775,h.top+51,'内部监听',16,'var(--muted)'),text(1210,h.top+51,'主机发布',16,'var(--muted)'));
  const nets=h.networks.map((name,i)=>({name,y:h.top+122+i*106}));
  // Draw relationships before nodes so paths never paint over labels or icons.
  for(const g of h.groups){if(g.isPod)world.append(svg('rect',{x:318,y:g.top,width:796,height:g.height,rx:16,fill:'var(--pod-fill)',stroke:'var(--pod-border)','stroke-dasharray':'6 5'}));for(const name of g.networks){const n=nets.find(n=>n.name===name);if(n)line(world,272,n.y,330,g.isPod?g.top+25:g.items[0].center,'var(--network)',true);}
   for(const item of g.items){const e=item.entry;for(const p of item.ports){const l=p.listener,color=conflictIDs.has(e.id)?'var(--red)':l.status==='reserved'?'var(--amber)':l.protocol==='udp'?'var(--purple)':l.protocol==='both'?'var(--network)':'var(--blue)';
    line(world,650,item.center,740,p.y,color);for(let i=0;i<l.publications.length;i++){const pub=l.publications[i],py=p.y-p.height/2+34+i*68;line(world,1080,p.y,1160,py,pub.status==='released'||l.status==='released'?'var(--muted)':color);}
   }}
  }
  for(const n of nets)world.append(node(32,n.y-38,240,76,'network',n.name,'自定义网络 · 点击筛选',{color:'var(--network)',max:17,onClick:()=>onNetwork?.(n.name)}));
  if(!nets.length)world.append(text(44,h.top+137,'尚未登记自定义网络',16,'var(--muted)'));
  for(const g of h.groups){
   if(g.isPod){world.append(icon('pod',333,g.top+11,'var(--blue)'),text(375,g.top+31,'Pod '+g.members[0].pod+' · 共享网络命名空间',17,'var(--muted)',550));}
   for(const item of g.items){const e=item.entry,cls=(conflictIDs.has(e.id)?'conflict ':'')+(e.id==='__draft'?'draft':''),edit=()=>onEdit?.(e.id);
    world.append(node(340,item.center-39,310,78,'container',e.container,e.scope==='host'?'host 网络 · 主机直接监听':g.isPod?'Pod 内容器':e.networks.length?'独立容器 · 已加入网络':'独立容器',{cls,onClick:edit,max:23}));
    for(const p of item.ports){const l=p.listener,color=l.protocol==='udp'?'var(--purple)':l.protocol==='both'?'var(--network)':'var(--blue)',dim=l.status==='released';
     const n=node(740,p.y-36,340,72,'port',`${range(l.port_start,l.port_end)} · ${protocolLabel(l.protocol)}`,l.service||'内部监听',{cls,onClick:edit,color,label:`${e.container}:${range(l.port_start,l.port_end)} / ${protocolLabel(l.protocol)} · ${l.listen_ip} · ${l.service} · ${l.status}`,max:28});
     if(dim)n.setAttribute('opacity','.45');world.append(n);world.append(text(750,p.y+53,`${l.listen_ip}${l.status==='reserved'?' · 预留':dim?' · 已释放':''}`,13,'var(--muted)'));
     if(!l.publications.length)world.append(text(1180,p.y+5,e.scope==='host'?'直接监听主机端口':'仅内部 · 无主机映射',16,'var(--muted)'));
     for(let i=0;i<l.publications.length;i++){const pub=l.publications[i],py=p.y-p.height/2+34+i*68,m=node(1160,py-29,330,58,'host',`${range(pub.host_start,pub.host_end)} · ${protocolLabel(pub.protocol)}`,`${pub.host_ip} · ${pub.status==='reserved'?'预留':pub.status==='released'?'已释放':'对外映射'}`,{cls,onClick:edit,color,max:28});if(dim||pub.status==='released')m.setAttribute('opacity','.45');world.append(m);}
    }
   }
  }
 }
 canvas.append(root);return{svg:root,world,height,width:1530};
}
