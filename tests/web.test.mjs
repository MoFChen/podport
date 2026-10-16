import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,writeFileSync} from 'node:fs';
import {webcrypto} from 'node:crypto';
import {parseHTML} from 'linkedom';
import {blank,blankListener,blankPublication,demoEntries,normalize,conflicts,mergeEntry,parseRange,matches,reachable,validateImport} from '../web/model.js';
import {renderGraph,graphCSS} from '../web/graph.js';
if(!globalThis.crypto)Object.defineProperty(globalThis,'crypto',{value:webcrypto});
const copy=v=>JSON.parse(JSON.stringify(v));
test('nginx Pod and independent emby share a custom network and retain different namespaces',()=>{
 const all=demoEntries(),[nginx,,emby]=all;for(const e of all)assert.equal(conflicts(e,all).length,0);
 assert.ok(reachable(nginx,emby,emby.listeners[0]));assert.equal(emby.listeners[0].publications.length,0);
 const independent=normalize({...emby,id:'other',container:'other',listeners:[{...blankListener(),port_start:80}]});assert.equal(conflicts(independent,all).length,0);
 assert.ok(!reachable({...nginx,host:'other'},emby,emby.listeners[0]));assert.ok(!reachable(nginx,emby,{...emby.listeners[0],listen_ip:'127.0.0.1'}));
 assert.ok(reachable(all[0],all[1],{...all[1].listeners[0],listen_ip:'127.0.0.1'}));
});
test('multiple internal listeners and multiple published mappings have independent protocols',()=>{
 const e=normalize({...blank(),container:'sync',listeners:[{...blankListener(),port_start:22000,protocol:'both',publications:[{...blankPublication('both'),host_start:22000},{...blankPublication('tcp'),host_start:22001}]},{...blankListener(),port_start:8384}]});assert.equal(conflicts(e,[]).length,0);
 for(const protocol of['tcp','udp']){const b=normalize({...blank(),container:'other',listeners:[{...blankListener(),port_start:90,protocol,publications:[{...blankPublication(protocol),host_start:22000}]}]});assert.ok(conflicts(b,[e]).length);}
 const b=normalize({...blank(),container:'other',listeners:[{...blankListener(),port_start:90,protocol:'udp',publications:[{...blankPublication('udp'),host_start:22001}]}]});assert.equal(conflicts(b,[e]).length,0);
});
test('self conflicts, shared Pod conflicts and publication release are distinct',()=>{
 const all=demoEntries(),e=copy(all[0]);e.id='other';e.container='other';e.listeners[0].publications=[];e.listeners=e.listeners.slice(0,1);assert.ok(conflicts(e,all).some(c=>c.kind==='pod'));
 e.scope='container';e.pod='';e.listeners.push({...e.listeners[0],id:'dup'});assert.ok(conflicts(e,[]).length);
 const a=copy(all[0]);a.listeners[0].publications[0].status='released';const b=normalize({...blank(),host:'nas',container:'new',listeners:[{...blankListener(),port_start:88,publications:[{...blankPublication(),host_start:8080}]}]});assert.equal(conflicts(b,[a]).length,0);
 a.listeners[0].status='released';assert.equal(conflicts(e,[a]).filter(c=>c.with_id===a.id).length,0);
});
test('Pod membership update propagates only to Pod members; duplicate container is rejected',()=>{
 const all=demoEntries(),e={...all[0],networks:['backend','media-net']},merged=mergeEntry(e,all);assert.deepEqual(merged.find(e=>e.id==='api').networks,['backend','media-net']);assert.deepEqual(merged.find(e=>e.id==='emby').networks,['media-net']);assert.throws(()=>mergeEntry({...all[0],id:'duplicate',listeners:[]},all),/唯一/);
});
test('ranges, network search and per-container queries include every port',()=>{
 assert.deepEqual(parseRange('8000-8010'),[8000,8010]);for(const v of['0','65536','80-79','1e3'])assert.throws(()=>parseRange(v));const all=demoEntries();assert.ok(matches(all[0],'443','',''));assert.ok(matches(all[0],'8443','',''));assert.ok(matches(all[2],'media-net','',''));assert.ok(matches(all[3],'22000','','','sync-net'));assert.ok(!matches(all[3],'22000','','','media-net'));
 const e=copy(all[2]);e.listeners[0].port_start=8000;e.listeners[0].port_end=8010;assert.ok(matches(e,'8005','',''));
});
test('v2 JSON imports merge a container and preserve released publications',()=>{
 const base={id:'one',host:'nas',pod:'web',container:'nginx',scope:'pod',listen_ip:'0.0.0.0',port_start:80,port_end:80,protocol:'tcp',status:'active',service:'web',host_ip:'0.0.0.0',host_start:8080,host_end:8080};const out=validateImport({schema_version:2,entries:[base,{...base,id:'two',host_start:8181,host_end:8181,status:'released'},{...base,id:'three',port_start:443,port_end:443,host_start:0,host_end:0}]});assert.equal(out.length,1);assert.equal(out[0].listeners.length,2);assert.equal(out[0].listeners[0].publications[1].status,'released');assert.throws(()=>validateImport({schema_version:3,entries:[out[0],{...out[0],id:'dup'}]}));
});
test('bad protocol subset, IPv6, host-network memberships and overlong values are rejected',()=>{
 const e=demoEntries()[0];for(const mutate of[e=>e.listeners[0].publications[0].protocol='both',e=>e.listeners[0].listen_ip='::',e=>e.scope='host',e=>e.container='x'.repeat(101)]){const copy=structuredClone(e);mutate(copy);assert.throws(()=>normalize(copy));}
});
const light={'--surface':'#ffffff','--canvas':'#f8fafd','--text':'#26354f','--muted':'#697991','--border':'#dce3ee','--blue':'#3f64d8','--purple':'#9364c5','--network':'#228b85','--red':'#cb526b','--amber':'#ad7a25','--tint':'#edf2fd','--pod-fill':'#f0f4fb','--pod-border':'#b7c9e7'};
const dark={'--surface':'#192333','--canvas':'#141e2c','--text':'#e3eaf6','--muted':'#a2b1c9','--border':'#334158','--blue':'#8caaff','--purple':'#c49beb','--network':'#60c7bd','--red':'#f38a9f','--amber':'#e4b96f','--tint':'#26364e','--pod-fill':'#202f43','--pod-border':'#506789'};
test('single-page UI edits multiple ports, shared networks, both protocols, themes and draft conflicts',async()=>{
 const{window}=parseHTML(readFileSync('web/index.html','utf8')),doc=window.document;
 Object.defineProperty(window.HTMLSelectElement.prototype,'value',{get(){return this.__value??this.querySelector('option[selected]')?.getAttribute('value')??this.querySelector('option')?.getAttribute('value')??''},set(v){this.__value=String(v)},configurable:true});
 for(const d of doc.querySelectorAll('dialog')){d.showModal=()=>{d.open=true;d.setAttribute('open','')};d.close=()=>{d.open=false;d.removeAttribute('open');};}
 const prefs=new Map();Object.assign(globalThis,{window,document:doc,confirm:()=>true,localStorage:{setItem:(k,v)=>prefs.set(k,v)},getComputedStyle:()=>({getPropertyValue:k=>(doc.documentElement.dataset.theme==='dark'?dark:light)[k]}),XMLSerializer:class{serializeToString(n){return n.outerHTML;}}});window.PODPORT_CONFIG={mode:'demo'};
 const{ready}=await import('../web/app.js?test=3');await ready;
 const $=id=>doc.getElementById(id),tick=()=>new Promise(r=>setTimeout(r,20)),event=(el,type='input')=>el.dispatchEvent(new window.Event(type,{bubbles:true})),fill=(name,value)=>{const el=$('entry-form').querySelector(`[name="${name}"]`);el.value=value;event(el);},port=(card,key,value)=>{const el=card.querySelector(`[data-field="${key}"]`);el.value=value;event(el);};
 assert.equal(doc.querySelectorAll('tbody tr').length,4);assert.equal(doc.querySelector('aside,nav'),null);
 $('search').value='8096';event($('search'));assert.equal(doc.querySelectorAll('tbody tr').length,1);$('search').value='';event($('search'));$('network-filter').value='media-net';event($('network-filter'),'change');assert.equal(doc.querySelectorAll('tbody tr').length,3);$('network-filter').value='';event($('network-filter'),'change');
 $('theme-select').value='dark';event($('theme-select'),'change');assert.equal(doc.documentElement.dataset.theme,'dark');assert.equal(prefs.get('podport-theme'),'dark');
 $('add').click();for(const[k,v]of Object.entries({host:'nas',container:'new-app',networks:'media-net'}))fill(k,v);let card=$('listeners').firstElementChild;port(card,'ports','7777');port(card,'protocol','both');card.querySelector('[data-action="add-publication"]').click();let pub=card.querySelector('.publication-grid');port(pub,'host_ports','7777');assert.equal(pub.querySelector('[data-field="protocol"]').value,'both');card.querySelector('[data-action="add-publication"]').click();pub=card.querySelectorAll('.publication-grid')[1];port(pub,'host_ports','7787');port(pub,'protocol','tcp');$('add-listener').click();card=$('listeners').lastElementChild;port(card,'ports','9001');port(card,'service','admin');
 $('preview').click();assert.ok(!$('editor').open);assert.ok(!$('save-draft').disabled);assert.match($('graph-canvas').textContent,/7777 · TCP \+ UDP/);assert.ok(doc.querySelectorAll('svg .node-icon path').length>0);assert.doesNotMatch($('graph-canvas').textContent,/[◇▱▤]/);
 for(const n of doc.querySelectorAll('.g-node')){const icon=n.querySelector('.node-icon'),box=n.querySelector('.node-box');if(!icon)continue;assert.equal(+icon.getAttribute('y')+13,+box.getAttribute('height')/2);assert.ok(+icon.getAttribute('x')+26<65);}
 $('save-draft').click();await tick();$('list-view').click();assert.equal(doc.querySelectorAll('tbody tr').length,5);let row=[...doc.querySelectorAll('tbody tr')].find(r=>r.dataset.id==='api');
 row=[...doc.querySelectorAll('tbody tr')].find(r=>r.dataset.id==='nginx');row.querySelector('[data-action="edit"]').click();fill('networks','backend, media-net');$('entry-form').dispatchEvent(new window.Event('submit',{cancelable:true,bubbles:true}));await tick();row=[...doc.querySelectorAll('tbody tr')].find(r=>r.dataset.id==='api');assert.match(row.textContent,/backend/);
 $('add').click();for(const[k,v]of Object.entries({host:'nas',pod:'web',container:'conflict',networks:'backend, media-net'}))fill(k,v);port($('listeners').firstElementChild,'ports','80');$('preview').click();assert.ok($('save-draft').disabled);assert.ok(doc.querySelectorAll('.g-node.conflict').length>0);assert.match($('draft-label').textContent,/冲突/);$('cancel-draft').click();
 $('add').click();for(const[k,v]of Object.entries({host:'nas',container:'nginx'}))fill(k,v);$('listeners').firstElementChild.querySelector('[data-action="remove-listener"]').click();$('preview').click();assert.ok($('save-draft').disabled);assert.match($('draft-label').textContent,/唯一/);$('cancel-draft').click();
 $('add').click();fill('container','<img src=x onerror=alert(1)>');$('listeners').firstElementChild.querySelector('[data-action="remove-listener"]').click();$('entry-form').dispatchEvent(new window.Event('submit',{cancelable:true,bubbles:true}));await tick();$('list-view').click();assert.equal(doc.querySelectorAll('tbody img').length,0);assert.match($('rows').textContent,/<img src=x/);row=[...doc.querySelectorAll('tbody tr')].find(r=>r.textContent.includes('<img src=x'));row.querySelector('[data-action="delete"]').click();await tick();assert.equal(doc.querySelectorAll('tbody tr').length,5);
 row=[...doc.querySelectorAll('tbody tr')].find(r=>r.textContent.includes('new-app'));row.querySelector('[data-action="edit"]').click();assert.equal($('listeners').children.length,2);card=$('listeners').firstElementChild;assert.equal(card.querySelectorAll('.publication-grid').length,2);port(card,'status','released');card.querySelectorAll('.publication-grid')[1].querySelector('[data-action="remove-publication"]').click();$('entry-form').dispatchEvent(new window.Event('submit',{cancelable:true,bubbles:true}));await tick();assert.match($('rows').textContent,/已释放/);
 // Export samples for static SVG rendering, independent of browser availability.
 for(const[theme,colors]of Object.entries({light,dark})){const canvas=doc.createElement('div'),g=renderGraph(canvas,demoEntries());const style=doc.createElementNS('http://www.w3.org/2000/svg','style');style.textContent=graphCSS;g.svg.prepend(style);let raw=g.svg.outerHTML.replace(/var\((--[\w-]+)\)/g,(_,k)=>colors[k]);raw=raw.replace('<svg ','<svg width="1530" height="'+g.height+'" ');writeFileSync('../artifacts/topology-'+theme+'.svg',raw);}
});
