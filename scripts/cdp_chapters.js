const http=require('http'),fs=require('fs'),path=require('path'),{spawn}=require('child_process');
const ROOT=process.env.ECHO_ROOT||process.cwd(); const TMP=path.join(ROOT,'.tmp');
const base=process.env.ECHO_BASE_URL||'http://127.0.0.1:8090', jar=path.join(TMP,'echo.cookies'), port=9227;
const profile=path.join(TMP,'chrome-chapters'); fs.mkdirSync(profile,{recursive:true});
const chrome=spawn('google-chrome',['--headless=new','--disable-gpu','--no-sandbox',`--user-data-dir=${profile}`,`--remote-debugging-port=${port}`,'--window-size=1280,900','about:blank'],{stdio:'ignore'});
const sleep=(ms)=>new Promise(r=>setTimeout(r,ms));
function getJSON(p){return new Promise((res,rej)=>{http.get({host:'127.0.0.1',port,path:p},r=>{let d='';r.on('data',x=>d+=x);r.on('end',()=>{try{res(JSON.parse(d))}catch(e){rej(e)}})}).on('error',rej)})}
(async()=>{let t=null;for(let i=0;i<50;i++){try{const l=await getJSON('/json/list');t=l.find(x=>x.type==='page');if(t)break}catch(e){}await sleep(200)}
const ws=new WebSocket(t.webSocketDebuggerUrl);const pend=new Map();let seq=0;const events=[];const call=(m,p={})=>new Promise(r=>{const id=++seq;pend.set(id,r);ws.send(JSON.stringify({id,method:m,params:p}))});
ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id&&pend.has(m.id)){pend.get(m.id)(m.result);pend.delete(m.id)}if(m.method==='Runtime.exceptionThrown'){const d=m.params.exceptionDetails;events.push('EXCEPTION: '+((d.exception&&d.exception.description)||d.text))}};
await new Promise(r=>{ws.onopen=r});await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
const cookie=fs.readFileSync(jar,'utf8').split('\n').find(l=>l.includes('echo_session')).split('\t').pop().trim();
await call('Network.setCookie',{name:'echo_session',value:cookie,url:base,path:'/',httpOnly:true});
await call('Page.navigate',{url:base+'/?_='+Date.now()+'#/audiobooks/8'});await sleep(4500);
const state=await call('Runtime.evaluate',{expression:`JSON.stringify((()=>{
  const audio=document.getElementById('ab-audio');
  return {
    chapterCount: document.querySelectorAll('#chapter-player li').length,
    firstTitle: document.querySelector('#chapter-player .chapter-title')?.textContent,
    activeIndex: document.querySelector('#chapter-player li.active')?.dataset.index,
    audioSrc: audio?.getAttribute('src'),
    subtitle: document.getElementById('subtitle-now')?.textContent,
    transcriptCount: document.querySelectorAll('#transcript-panel li').length
  };
})())`,returnByValue:true});
console.log('DETAIL', state.result.value);
const shot=await call('Page.captureScreenshot',{format:'png'});
fs.mkdirSync(path.join(TMP,'ui'),{recursive:true});fs.writeFileSync(path.join(TMP,'ui','ui-chapters.png'),Buffer.from(shot.data,'base64'));
console.log('saved ui-chapters'); console.log('EVENTS:', events.length?events.join('\n'):'(none)');
chrome.kill();process.exit(0)})().catch(e=>{console.error(e);chrome.kill();process.exit(1)});
