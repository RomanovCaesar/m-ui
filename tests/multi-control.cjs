const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { chromium } = require(process.env.MUI_PLAYWRIGHT || 'playwright');

const artifacts=path.resolve('.runtime-import');
fs.mkdirSync(artifacts,{recursive:true});
const testDir=fs.mkdtempSync(path.join(artifacts,'mesh-ui-'));
const fixture=path.resolve(process.env.MUI_PEER_FIXTURE||'.runtime-import/mesh-fixture.exe');
const processes=[];
const ports=[21550,21551,21552];
function start(i){
  const proc=spawn(fixture,['-test.run=^TestMultiControlBrowserFixture$','-test.timeout=10m'],{
    cwd:path.resolve('.'),windowsHide:true,env:{...process.env,MUI_PEER_BROWSER_ADDR:'127.0.0.1:'+ports[i],MUI_PEER_BROWSER_DATA:path.join(testDir,String(i)),MUI_PEER_BROWSER_NAME:['Panel A','Panel B','Panel C'][i]},stdio:'ignore'
  });
  processes[i]=proc;return proc;
}
async function stop(i){const proc=processes[i];if(proc&&proc.exitCode===null){await new Promise(resolve=>{proc.once('exit',resolve);proc.kill();});}}
async function ready(i){
  for(let attempt=0;attempt<60;attempt++){
    if(processes[i].exitCode!==null)throw new Error('test panel exited before starting');
    try{const r=await fetch('http://127.0.0.1:'+ports[i]+'/testpath/login');if(r.status===200)return;}catch{}
    await new Promise(resolve=>setTimeout(resolve,100));
  }throw new Error('test panel failed to start');
}

(async()=>{
  let browser;
  try{
    for(let i=0;i<3;i++)start(i);
    await Promise.all(ports.map((_,i)=>ready(i)));
    browser=await chromium.launch({headless:true,executablePath:process.env.MUI_CHROMIUM});
    const pages=[],errors=[];
    for(let i=0;i<3;i++){
      const context=await browser.newContext({viewport:{width:1440,height:1000}});
      const page=await context.newPage();pages.push(page);page.on('pageerror',error=>errors.push(error.message));
      await page.goto('http://127.0.0.1:'+ports[i]+'/testpath/');
      await page.locator('#username').fill('admin');await page.locator('#password').fill('admin');await page.locator('#login-button').click();
      await page.locator('[data-view="settings"]').click();await page.locator('[data-settings-tab="multi-control"]').click();
      await page.waitForFunction(()=>document.querySelector('#mc-id').textContent.length===32);
      assert.deepEqual(await page.locator('#settings-tabbar [data-settings-tab]').evaluateAll(tabs=>tabs.map(tab=>tab.dataset.settingsTab)),['general','authentication','subscription','multi-control']);
      assert.equal(await page.locator('[data-settings-tab="subscription"] svg path').count(),3);
      assert.equal(await page.locator('[data-settings-tab="multi-control"] svg circle').count(),3);
      assert.equal(await page.locator('#view-settings>.page-head').count(),0);
      assert.equal(await page.locator('#view-settings>.mihomo-settings-page>.mihomo-actionbar').count(),1);
      assert.equal(await page.locator('#view-settings>.mihomo-settings-page>.mihomo-card').count(),1);
    }
    const [a,b,c]=pages;
    await b.locator('#mc-token-open').click();await b.locator('#mc-token-generate').click();
    await b.waitForFunction(()=>/^[a-z0-9]{16}$/.test(document.querySelector('#mc-token').value));
    const token=await b.locator('#mc-token').inputValue();assert.match(token,/[a-z]/);assert.match(token,/[0-9]/);
    await b.screenshot({path:path.join(artifacts,'multi-control-token.png')});
    await b.locator('#mc-token-modal [data-mc-close]').click();
    async function connect(page,wrong=false){
      await page.locator('#mc-connections-open').click();await page.locator('#mc-add-toggle').click();
      await page.locator('#mc-connect-form [name="host"]').fill('127.0.0.1');
      await page.locator('#mc-connect-form [name="port"]').fill(String(ports[1]));
      await page.locator('#mc-connect-form [name="token"]').fill(wrong?'wrongtoken123456':token);
      await page.locator('#mc-connect-submit').click();
      if(wrong){
        await page.locator('#mc-connections-modal .mc-modal-error').waitFor({state:'visible'});
        assert.equal(await page.locator('#mc-connect-form').isVisible(),true);
        await page.locator('#mc-connect-form [name="token"]').fill(token);await page.locator('#mc-connect-submit').click();
      }
      await page.locator('#mc-connect-form').waitFor({state:'hidden'});
      assert.equal(await page.locator('#mc-connect-form [name="token"]').inputValue(),'');
      assert.equal(await page.locator('#mc-connections-modal .mc-modal-error').isVisible(),false);
    }
    await connect(a,true);await connect(c);
    await a.waitForFunction(()=>document.querySelectorAll('#mc-peer-list tbody tr').length===2&&document.querySelectorAll('#mc-peer-list .online').length===2,{},{timeout:30000});
    await b.locator('#mc-connections-open').click();
    await b.waitForFunction(()=>document.querySelectorAll('#mc-peer-list .online').length===2,{},{timeout:30000});
    await a.screenshot({path:path.join(artifacts,'multi-control-peers.png')});
    await a.setViewportSize({width:390,height:844});await a.screenshot({path:path.join(artifacts,'multi-control-mobile.png')});
    const bounds=await a.locator('#mc-connections-modal .modal-panel').boundingBox();assert.ok(bounds.x>=0&&bounds.x+bounds.width<=390);
    const width=await a.locator('#mc-connections-modal .modal-panel').evaluate(el=>({outer:el.clientWidth,inner:el.scrollWidth}));assert.equal(width.inner,width.outer);
    await a.setViewportSize({width:1440,height:1000});
    const cID=await c.locator('#mc-id').textContent();
    const cRow=a.locator('#mc-peer-list tbody tr').filter({hasText:cID});
    a.once('dialog',dialog=>dialog.accept());await cRow.locator('[data-mc-disconnect]').click();
    await a.waitForFunction(()=>document.querySelectorAll('#mc-peer-list tbody tr').length===1);
    await a.locator('#mc-blocked-section summary').click();
    await a.locator('[data-mc-unblock]').click();
    await a.waitForFunction(()=>document.querySelectorAll('#mc-peer-list .online').length===2,{},{timeout:30000});
    await b.locator('#mc-connections-modal [data-mc-close]').click();await b.locator('#mc-token-open').click();await b.locator('#mc-token-disable').click();
    await b.waitForFunction(()=>document.querySelector('#mc-token').value==='');
    // B only introduced the network. A and C must keep heartbeating directly.
    await stop(1);
    await a.waitForFunction(()=>{
      const rows=Array.from(document.querySelectorAll('#mc-peer-list tbody tr'));
      return rows.length===2&&rows.some(row=>row.textContent.includes('Panel B')&&!row.querySelector('.online'))&&rows.some(row=>row.textContent.includes('Panel C')&&row.querySelector('.online'));
    },{},{timeout:65000});
    await a.screenshot({path:path.join(artifacts,'multi-control-offline-seed.png')});
    const originalID=await c.locator('#mc-id').textContent();
    await stop(2);start(2);await ready(2);await c.reload();
    await c.locator('[data-view="settings"]').click();await c.locator('[data-settings-tab="multi-control"]').click();
    await c.waitForFunction(()=>document.querySelector('#mc-id').textContent.length===32);
    assert.equal(await c.locator('#mc-id').textContent(),originalID);
    await c.locator('#mc-connections-open').click();
    await c.waitForFunction(()=>Array.from(document.querySelectorAll('#mc-peer-list tbody tr')).some(row=>row.textContent.includes('Panel A')&&row.querySelector('.online')),{},{timeout:30000});
    assert.deepEqual(errors,[]);
    console.log('PASS: three independent panels, token pairing, auto discovery, local disconnect/unblock, offline seed, restart identity and automatic reconnect, mobile layout');
  }finally{
    if(browser)await browser.close();
    await Promise.all(processes.map((_,i)=>stop(i)));
    // Only disposable test-panel data beneath the validated test directory.
    if(testDir.startsWith(artifacts+path.sep)&&path.basename(testDir).startsWith('mesh-ui-'))fs.rmSync(testDir,{recursive:true,force:true});
  }
})().catch(error=>{console.error(error);process.exitCode=1;});
