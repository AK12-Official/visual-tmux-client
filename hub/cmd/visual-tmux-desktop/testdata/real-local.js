(() => {
 const report=async(message='',failed=false)=>fetch('/api/desktop/fixture-real-local',{
  method:'POST',headers:{'Content-Type':'application/json',
  Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')},body:JSON.stringify({message,failed})});
 const wait=async(predicate,label)=>{
  for(let i=0;i<400;i++){if(predicate())return;await new Promise(r=>setTimeout(r,50));}
  const error=document.querySelector('.host-alert--danger')?.textContent?.trim()??'';
  throw Error('Timed out: '+label+' host='+document.querySelector('#host-select')?.value+' error='+error);
 };
 (async()=>{
  if(window.__vtcRealRound===0){
   const manage=()=>[...document.querySelectorAll('.app__bar-btn')].find(n=>/Manage remote hosts|管理远程主机/.test(n.getAttribute('aria-label')??''));
   await wait(manage,'host manager');manage().click();
   await wait(()=>document.querySelector('.host-modal__add-btn'),'add host');
   document.querySelector('.host-modal__add-btn').click();
   await wait(()=>document.querySelector('#host-field-id'),'host form');
   const details=window.__vtcEnrollment;
   for(const [key,value] of Object.entries({id:'remote',name:'Disposable SSH',address:details.address,
     port:details.port,username:details.username,password:details.password})){
    const input=document.querySelector('#host-field-'+key);
    input.value=String(value);input.dispatchEvent(new Event('input',{bubbles:true}));
   }
   await wait(()=>!document.querySelector('.host-probe__probe-btn').disabled,'probe enabled');
   document.querySelector('.host-probe__probe-btn').click();
   await wait(()=>document.querySelector('.host-probe-fingerprint'),'server fingerprint');
   if(document.querySelector('.host-probe-fingerprint').textContent.trim()!==details.fingerprint)throw Error('Wrong server fingerprint');
   document.querySelector('.host-probe__trust-btn').click();
   await wait(()=>document.querySelector('.host-probe-trusted-indicator'),'fingerprint trusted');
   document.querySelector('.host-form button[type=submit]').click();
   await wait(()=>!document.querySelector('#host-field-id'),'host saved');
   document.querySelector('.host-modal__close').click();
   await report('GUI host enrollment and exact fingerprint trust');
  }else{
   const manage=()=>[...document.querySelectorAll('.app__bar-btn')].find(n=>/Manage remote hosts|管理远程主机/.test(n.getAttribute('aria-label')??''));
   await wait(manage,'repair host manager');manage().click();
   const card=()=>[...document.querySelectorAll('.host-card')].find(n=>n.querySelector('.host-card__name')?.textContent==='Disposable SSH');
   await wait(card,'persisted remote card');
   const control=pattern=>[...card().querySelectorAll('button')].find(n=>pattern.test(n.getAttribute('aria-label')??''));
   control(/Test|测试/).click();
   await wait(()=>card().querySelector('.host-caps-error'),'old key rejected');
   if(!/key|密钥|指纹|fingerprint|trust|信任|host_unavailable/i.test(card().querySelector('.host-caps-error').textContent))throw Error('Expected host-key failure: '+card().querySelector('.host-caps-error').textContent);
   await report('rotated key rejected before repair');
   control(/Edit|编辑/).click();
   await wait(()=>document.querySelector('.host-probe__probe-btn'),'repair probe');
   document.querySelector('.host-probe__probe-btn').click();
   await wait(()=>document.querySelector('.host-probe-fingerprint'),'rotated fingerprint');
   if(document.querySelector('.host-probe-fingerprint').textContent.trim()!==window.__vtcEnrollment.fingerprint)throw Error('Wrong rotated fingerprint');
   document.querySelector('.host-probe__trust-btn').click();
   await wait(()=>document.querySelector('.host-probe-trusted-indicator'),'repaired trust');
   document.querySelector('.host-form button[type=submit]').click();
   await wait(()=>!document.querySelector('#host-field-id'),'repair saved');
   document.querySelector('.host-modal__close').click();
   await report('GUI repaired trust with exact rotated fingerprint');
  }

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
