// TEST_CASES: APPTHEME-T-002
// Source images and SVG artwork are reviewed inputs. Runtime themes contain
// canonical PNG data only. --render is an explicit asset-maintenance operation.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const check=process.argv.includes('--check'),render=process.argv.includes('--render');
if(check&&render)throw Error('choose --check or --render');
const hash=raw=>createHash('sha256').update(raw).digest('hex');
const names={original:{en:'Original', 'zh-CN':'原始图标','zh-TW':'原始圖示',ja:'オリジナル',ko:'원본',fr:'Originaux',de:'Original',es:'Originales'},soft:{en:'Minimal','zh-CN':'简约','zh-TW':'簡約',ja:'ミニマル',ko:'미니멀',fr:'Minimaliste',de:'Minimal',es:'Minimalista'}};
const themes=Object.fromEntries(Object.keys(names).map(id=>[id,{schema:'anas.application-theme/v1',id,name:names[id],icons:{},sources:{}}]));
let browser,page;
if(render){const {chromium}=await import('@playwright/test');browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});page=await browser.newPage();await page.route('**/*',r=>r.abort());}
async function raster(file){const raw=await fs.readFile(file),svg=file.endsWith('.svg');if(svg&&/<script|onload=|foreignObject|(?:href|url)\s*[=(]\s*["']?https?:/i.test(raw.toString()))throw Error('external or executable SVG: '+file);return Buffer.from(await page.evaluate(async({source,type})=>{const image=new Image();image.src='data:'+type+';base64,'+source;await image.decode();const c=document.createElement('canvas');c.width=c.height=512;const ratio=Math.min(512/image.naturalWidth,512/image.naturalHeight),w=image.naturalWidth*ratio,h=image.naturalHeight*ratio;c.getContext('2d').drawImage(image,(512-w)/2,(512-h)/2,w,h);return c.toDataURL('image/png').split(',')[1];},{source:raw.toString('base64'),type:svg?'image/svg+xml':'image/png'}),'base64');}
async function output(file,raw){if(check){const prior=await fs.readFile(file);if(!prior.equals(raw))throw Error('stale generated file: '+path.relative(root,file));}else await fs.writeFile(file,raw);}
try{
for(const module of (await fs.readdir(path.join(root,'modules'))).sort()){
 const dir=path.join(root,'modules',module),manifest=await fs.readFile(path.join(dir,'module.yml'),'utf8').catch(()=>''),match=manifest.match(/^applications:\n  - name: ([\w-]+)/m);
 if(!match)continue;
 const assetDir=path.join(dir,'assets'),metadataFile=path.join(assetDir,'icon-source.json'),record=JSON.parse(await fs.readFile(metadataFile));
 if(record.application!==match[1]||!manifest.includes('source: assets/'+record.output))throw Error('icon declaration mismatch: '+module);
 const source=await fs.readFile(path.join(assetDir,record.source.file));if(hash(source)!==record.source.sha256||!record.source.url.startsWith('https://'))throw Error('source provenance mismatch: '+module);
 const artwork=await fs.readFile(path.join(assetDir,record.artwork)),soft=await fs.readFile(path.join(root,record.soft_artwork));
 const originalFile=path.join(assetDir,record.output),softFile=path.join(root,'internal/apptheme/artwork',record.application+'.png');
 if(render){await fs.writeFile(originalFile,await raster(path.join(assetDir,record.artwork)));await fs.writeFile(softFile,await raster(path.join(root,record.soft_artwork)));record.artwork_sha256=hash(artwork);record.output_sha256=hash(await fs.readFile(originalFile));record.soft_artwork_sha256=hash(soft);record.soft_output_sha256=hash(await fs.readFile(softFile));await fs.writeFile(metadataFile,JSON.stringify(record,null,2)+'\n');}
 const original=await fs.readFile(originalFile),minimal=await fs.readFile(softFile);
 if(hash(artwork)!==record.artwork_sha256||hash(original)!==record.output_sha256||hash(soft)!==record.soft_artwork_sha256||hash(minimal)!==record.soft_output_sha256)throw Error('changed artwork requires --render: '+module);
 for(const [id,raw] of [['original',original],['soft',minimal]]){
  if(raw.readUInt32BE(16)!==512||raw.readUInt32BE(20)!==512)throw Error('non-square icon: '+module);
  themes[id].icons[record.application]=raw.toString('base64');themes[id].sources[record.application]={url:record.source.url,sha256:record.source.sha256,...(id==='soft'||record.generated?{generated:true}:{}),note:id==='soft'?'ANAS minimal geometric interpretation of the original mark, with a common plate and spacing.':record.method};
 }
}
for(const [id,theme] of Object.entries(themes))await output(path.join(root,'internal/apptheme/builtin',id+'.json'),Buffer.from(JSON.stringify(theme)+'\n'));
console.log('Application themes: '+Object.keys(themes.original.icons).length+' entries; provenance and both packages '+(check?'verified':'generated'));
}finally{await browser?.close();}
