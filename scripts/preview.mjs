import{mkdir,readdir,copyFile,writeFile}from'node:fs/promises';
await mkdir('dist',{recursive:true});
for(const file of await readdir('web'))await copyFile('web/'+file,'dist/'+file);
await writeFile('dist/config.js',"window.PODPORT_CONFIG={mode:'demo'};\n");
console.log('Packaged session-only preview; embedded production assets remain unchanged.');
