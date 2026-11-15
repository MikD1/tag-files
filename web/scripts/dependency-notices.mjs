import {readFileSync,readdirSync,statSync} from 'node:fs';
const lock=JSON.parse(readFileSync(new URL('../package-lock.json',import.meta.url),'utf8'));
function notices(path){
 const files=readdirSync(new URL('../'+path+'/',import.meta.url)).filter(name=>/^(license|copying|notice)([._-]|$)/i.test(name)).filter(name=>statSync(new URL('../'+path+'/'+name,import.meta.url)).isFile()).map(name=>({file:name,text:readFileSync(new URL('../'+path+'/'+name,import.meta.url),'utf8')}));
 if(!files.length&&path==='node_modules/react-remove-scroll-bar')files.push({file:'upstream LICENSE',text:readFileSync(new URL('../notices/react-remove-scroll-bar.LICENSE',import.meta.url),'utf8')});
 if(!files.length)throw new Error(`Missing distribution notice: ${path}`);
 return files;
}
const packages=Object.entries(lock.packages).filter(([path,pkg])=>path&&!pkg.dev).map(([path,pkg])=>({
 name:pkg.name??path.slice(path.lastIndexOf('node_modules/')+13),version:pkg.version,
 license:pkg.license??'SEE PACKAGE NOTICE',integrity:pkg.integrity,
 notices:notices(path),
}));
process.stdout.write(JSON.stringify({format:1,source:'npm package-lock.json',packages},null,2)+'\n');
