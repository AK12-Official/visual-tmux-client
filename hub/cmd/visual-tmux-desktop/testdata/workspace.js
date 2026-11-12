(() => {
 const wait = async (predicate,label) => {
  for(let i=0;i<150;i++){const value=predicate();if(value)return value;await new Promise(r=>setTimeout(r,50));}
  throw new Error('Timed out: '+label);
 };
 const action = async (name,body) => {
  const response=await fetch('/api/desktop/'+name,{method:'POST',
   headers:{'Content-Type':'application/json',Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')},
   body:JSON.stringify(body)});
  if(!response.ok)throw new Error('Fixture request failed: '+name+' '+JSON.stringify(body));
  return response.json();
 };
 const request = (phase,message='',extra={}) => action('fixture-workspace',{phase,message,...extra});
 const status = async (predicate,label) => {
  let last;
  for(let i=0;i<200;i++){
   const value=await request('status');last=value;if(predicate(value))return value;
   await new Promise(r=>setTimeout(r,50));
  }
  throw new Error('Native status not reached: '+label+' browser='+JSON.stringify([last?.browserHTTP,last?.browserHTTPS,last?.browserDone,last?.externalHits]));
 };
 const nativeInput = async initial => {
  const fullscreen=[...document.querySelectorAll('.app__term-btn')].find(b=>b.textContent.trim()==='⛶');
  fullscreen.click();
  await status(value=>value.fullscreen&&value.resizes>initial.resizes,'fullscreen refit');
  await wait(()=>document.querySelector('.app__main--desktop-fullscreen'),'fullscreen layout');
  fullscreen.click();
  await status(value=>!value.fullscreen,'fullscreen exit');
  await wait(()=>!document.querySelector('.app__main--desktop-fullscreen'),'fullscreen layout exit');
  const sized=await request('status');await request('resize');
  await status(value=>value.resizes>sized.resizes &&
   (value.cols!==sized.cols||value.rows!==sized.rows),'coalesced rapid resize');
  await new Promise(r=>setTimeout(r,500));
  document.querySelector('.xterm-helper-textarea').focus();
  await request('key-copy');
  await status(value=>value.input.includes('\u0003'),'Ctrl+C input');
  await action('clipboard',{text:'paste-中文\nline2\n'});
  const clipboard=await request('clipboard-status');
  if(clipboard.text!=='paste-中文\nline2\n')throw new Error('Native clipboard bytes differ: '+clipboard.text);
  await request('key-paste');
  await status(value=>value.input.replaceAll('\r','\n').includes('paste-中文\nline2\n'),'multiline paste');
  await request('commit');
  await status(value=>value.input.includes('输入-中文'),'native Unicode input commit');
  await status(value=>value.frames>value.rows+20,'scrollback output');
  await request('pause');
  await new Promise(r=>setTimeout(r,100));
  const screen=document.querySelector('.xterm-screen').getBoundingClientRect();
  const firstLine=()=>Number(document.querySelector('.xterm-rows')?.firstElementChild?.textContent.match(/stream-中文-(\d+)/)?.[1]);
  const bottom=firstLine();
  if(!Number.isFinite(bottom))throw new Error('Styled Unicode output was not rendered');
  await request('pointer','',{x:screen.x+screen.width/2,y:screen.y+screen.height/2});
  await request('wheel-up');
  await wait(()=>firstLine()<bottom,'native mouse wheel scroll up');
  const scrolled=firstLine();
  await request('wheel-down');
  await wait(()=>firstLine()>scrolled,'native mouse wheel scroll down');
  await new Promise(r=>setTimeout(r,200));
  const dimensions=await request('status');
  const cell=screen.width/dimensions.cols,row=screen.height/dimensions.rows;
  await request('drag','',{x:screen.x+cell*.2,y:screen.y+row*.5,
   endX:screen.x+cell*18,endY:screen.y+row*.5});
  await request('key-copy');
  await new Promise(r=>setTimeout(r,200));
  const copied=await request('clipboard-status');
  if(!copied.text.includes('stream-中文-'))throw new Error('Selected terminal text was not copied: '+copied.text);
  const copiedState=await request('status');
  if(copiedState.input.split('\u0003').length!==dimensions.input.split('\u0003').length)
   throw new Error('Copy selection sent Ctrl+C to terminal');
  const originalFetch=window.fetch;
  try {
   window.fetch=(url,options)=>String(url)==='/api/desktop/clipboard'
    ? Promise.resolve(new Response('fixture clipboard unavailable',{status:503}))
    : originalFetch(url,options);
   await request('key-copy');
   await wait(()=>[...document.querySelectorAll('.toast__message')].some(node=>
    node.textContent.includes('Copy failed')||node.textContent.includes('复制失败')),'visible clipboard failure');
  } finally {window.fetch=originalFetch;}
  await request('resume');
  await request('checkpoint','fullscreen/refit, Ctrl+C, clipboard/paste, native Unicode commit, wheel scroll and selected-text copy');
 };
 const run = async () => {
  (await wait(()=>document.querySelector('.session-list__row'),'session')).click();
  await wait(()=>document.querySelector('.app__term-state--connected'),'attachment');
  const initial=await status(value=>value.frames>2,'stream');
  if(initial.privateInput){await nativeInput(initial);await externalLinks(initial);}
  for(const approved of [false,true]){
   await request('overwrite','',{approved});
   const result=await action('download',{hostId:'local',connectionRevision:1,path:initial.sourcePath});
   if(result.cancelled===approved)throw new Error('Overwrite cancellation mismatch');
   await request('overwrite-result','',{approved});
  }
  await request('checkpoint','native overwrite cancel preserves destination; confirmation replaces exact bytes');
  (await wait(()=>document.querySelector('.app__term-btn[aria-pressed]'),'files button')).click();
  const markdown=await wait(()=>[...document.querySelectorAll('.tree__label')].find(b=>b.textContent.trim()==='README.md'),'markdown file');
  markdown.click();
  (await wait(()=>[...document.querySelectorAll('.fm__btn')].find(b=>['Preview','预览'].includes(b.textContent.trim())),'markdown preview button')).click();
  await wait(()=>[...document.querySelectorAll('h1')].some(node=>node.textContent==='Preview-中文'),'rendered Markdown preview');
  const file=await wait(()=>[...document.querySelectorAll('.tree__label')].find(b=>b.textContent.trim()==='draft.txt'),'file');
  file.click();
  const editor=await wait(()=>[...document.querySelectorAll('.cm-content')].find(node=>node.getBoundingClientRect().height>0&&node.textContent.startsWith('original')),'draft editor');
  editor.focus();
  const selection=window.getSelection();selection.selectAllChildren(editor);
  document.execCommand('insertText',false,'saved-中文\n');
  await wait(()=>document.querySelector('.tabs__dirty'),'saveable dirty state');
  const save=await wait(()=>[...document.querySelectorAll('.fm__btn')].find(b=>['Save','保存'].includes(b.textContent.trim())&&!b.disabled),'save button');
  save.click();
  await wait(()=>!document.querySelector('.tabs__dirty'),'saved clean state');
  await request('saved');
  await request('checkpoint','Markdown preview and Unicode editor save reached the private source file');
  editor.focus();selection.selectAllChildren(editor);
  document.execCommand('insertText',false,'unsaved-中文');
  await wait(()=>document.querySelector('.tabs__dirty'),'dirty state');
  const before=await request('status');
  await request('cancel');
  await new Promise(r=>setTimeout(r,800));
  if(!editor.isConnected||!editor.textContent.includes('unsaved-中文'))throw new Error('Draft lost on cancel');
  if(!document.querySelector('.app__term-state--connected'))throw new Error('Attachment lost on cancel');
  const after=await request('status');
  if(after.created!==1||after.closed!==0||after.frames<=before.frames)throw new Error('Stream changed on cancel');
  await request('discard');
 };
 const externalLinks = async initial => {
  const origin=location.origin;
  const link=document.createElement('a');
  link.href=initial.httpURL;link.textContent='Private external-link fixture';
  Object.assign(link.style,{position:'fixed',left:'10px',top:'10px',width:'260px',height:'35px',zIndex:'1000000'});
  document.body.appendChild(link);
  link.click();
  await new Promise(r=>setTimeout(r,150));
  const synthetic=await request('status');
  if(synthetic.browserHTTP||synthetic.browserHTTPS||synthetic.externalHits)throw new Error('Synthetic link invoked browser');
  for(const [url,field] of [[initial.httpURL,'browserHTTP'],[initial.httpsURL,'browserHTTPS']]){
   link.href=url;
   const rect=link.getBoundingClientRect();
   await request('pointer','',{x:rect.x+rect.width/2,y:rect.y+rect.height/2});
   await status(value=>value[field] && (field!=='browserHTTP'||value.externalHits>0),'system browser '+field+' '+document.getElementById('global-error')?.textContent);
   if(location.origin!==origin||!document.querySelector('.app__bar'))throw new Error('External navigation replaced workspace');
  }
  await status(value=>value.browserDone>=2,'owned browser completion');
  link.href='javascript:window.__unsupportedLinkRan=true';
  const rect=link.getBoundingClientRect();
  await request('pointer','',{x:rect.x+rect.width/2,y:rect.y+rect.height/2});
  await new Promise(r=>setTimeout(r,200));
  if(window.__unsupportedLinkRan)throw new Error('Unsupported URL executed');
  link.remove();
  await request('checkpoint','trusted HTTP/HTTPS system-browser routing, synthetic link and unsupported URL refusal');
 };
 run().catch(async error=>{
  document.title='Workspace fixture failed: '+error.message;
  console.error(error.message);
  await request('error',error.message);
 });
})()
