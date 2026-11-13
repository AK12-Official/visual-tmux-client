(() => {
 const report=async(message='',failed=false)=>fetch('/api/desktop/fixture-real-local',{
  method:'POST',headers:{'Content-Type':'application/json',
  Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')},body:JSON.stringify({message,failed})});
 const wait=async(predicate,label)=>{
  for(let i=0;i<400;i++){if(predicate())return;await new Promise(r=>setTimeout(r,50));}
  throw Error('Timed out: '+label+' host='+document.querySelector('#host-select')?.value);
 };
 (async()=>{
  for(const [id,session,marker] of [['local','real-gui','REAL_GUI_中文'],['remote','remote-gui','REAL_REMOTE_中文']]){
   await wait(()=>document.querySelector('#host-select'),'host selector');
   await report('select '+id);
   const select=document.querySelector('#host-select');
   select.value=id;select.dispatchEvent(new Event('change',{bubbles:true}));
   await wait(()=>[...document.querySelectorAll('.session-list__row')].some(n=>n.textContent.includes(session)),'session '+session);
   await report('attach '+id);
   [...document.querySelectorAll('.session-list__row')].find(n=>n.textContent.includes(session)).click();
   await wait(()=>document.querySelector('.app__term-state--connected')&&
    [...document.querySelectorAll('.xterm-rows')].some(n=>n.getBoundingClientRect().height>0&&n.textContent.includes(marker)),
    'visible output '+id);
   await report('rendered '+id);
   if(id==='remote'){
    await report('toggle remote files');
    document.querySelector('.app__term-btn[aria-pressed]').click();
    await wait(()=>[...document.querySelectorAll('.tree__label')].some(n=>n.textContent.trim()==='README.md'),'remote file tree');
    [...document.querySelectorAll('.tree__label')].find(n=>n.textContent.trim()==='README.md').click();
    await report('remote README opened');
    const button=label=>[...document.querySelectorAll('.fm__btn')].find(n=>label.includes(n.textContent.trim()));
    await wait(()=>button(['Preview','预览']),'remote preview button');button(['Preview','预览']).click();
    await wait(()=>[...document.querySelectorAll('h1')].some(n=>n.textContent==='Remote-中文'),'remote preview');
    [...document.querySelectorAll('.tree__label')].find(n=>n.textContent.trim()==='draft.txt').click();
    await report('remote draft opened');
    const editor=()=>[...document.querySelectorAll('.cm-content')].find(n=>n.getBoundingClientRect().height>0);
    await wait(()=>editor()?.textContent.includes('remote-'),'remote editor');
    editor().focus();window.getSelection().selectAllChildren(editor());
    document.execCommand('insertText',false,'remote-saved-中文-'+window.__vtcRealRound+'\n');
    await wait(()=>button(['Save','保存'])&&!button(['Save','保存']).disabled,'remote dirty save');
    await report('remote save requested');
    button(['Save','保存']).click();await wait(()=>!document.querySelector('.tabs__dirty'),'remote saved');
    [...document.querySelectorAll('.tree__label')].find(n=>n.textContent.trim()==='fixture.bin').click();
    await wait(()=>button(['Download','下载']),'binary download button');
    await report('remote download requested');button(['Download','下载']).click();
    let downloaded=false;
    for(let i=0;i<200;i++){
     if((await report('remote download result')).ok){downloaded=true;break;}
     await new Promise(r=>setTimeout(r,50));
    }
    if(!downloaded)throw Error('remote download bytes differ');
    document.querySelector('.app__term-btn[aria-pressed]').click();
    await report('remote Markdown preview and Unicode SFTP save');
    const visible=()=>[...document.querySelectorAll('.xterm-rows')].find(n=>n.getBoundingClientRect().height>0);
    const count=()=>Math.max(0,...[...(visible()?.textContent??'').matchAll(/REAL_REMOTE_中文_(\d+)/g)].map(m=>Number(m[1])));
    const before=count();
    let disconnected=false;
    const observe=()=>{if(!document.querySelector('.app__term-state--connected'))disconnected=true;};
    const observer=new MutationObserver(observe);
    observer.observe(document.body,{subtree:true,attributes:true,childList:true});
    if(!(await report('drop remote')).ok)throw Error('drop failed');
    await wait(()=>{
     const reconnect=document.querySelector('.terminal-view__reconnect');
     if(reconnect){disconnected=true;reconnect.click();}
     return disconnected;
    },'remote disconnect or automatic retry');
    await wait(()=>document.querySelector('.app__term-state--connected')&&
     visible()?.textContent.includes(marker)&&count()>before,'fresh remote output after reconnect');
    observer.disconnect();
    await report('reconnected remote with fresh output');
   }
  }
  if(!(await report()).ok)throw Error('real fixture completion failed');
 })().catch(error=>report(String(error),true));
})();
