// Local visual/interaction fixture; serves actual application styles and script.
const http = require('node:http'), fs = require('node:fs'), path = require('node:path');
const root = path.join(__dirname,'..'), web = path.join(root,'web');
const index = fs.readFileSync(path.join(web,'index.html'),'utf8');
const style = index.match(/<style>[\s\S]*?<\/style>/)[0];
http.createServer((req,res)=>{
  const url = new URL(req.url,'http://localhost');
  res.setHeader('Cache-Control','no-store');
  if(url.pathname==='/'){
    res.setHeader('Content-Type','text/html; charset=utf-8');
    res.end(fs.readFileSync(path.join(__dirname,'liquid-toggle.html'),'utf8').replace('<!--APP_STYLES-->',style));return;
  }
  const name = url.pathname==='/login' ? 'login.html' : url.pathname.replace(/^\/static\//,'');
  const file = path.resolve(web,name);
  if(!file.startsWith(web+path.sep)||!fs.existsSync(file)||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return;}
  const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png'};
  res.setHeader('Content-Type',(types[path.extname(file)]||'application/octet-stream')+'; charset=utf-8');res.end(fs.readFileSync(file));
}).listen(21864,'127.0.0.1',()=>console.log('Toggle fixture: http://127.0.0.1:21864/'));
