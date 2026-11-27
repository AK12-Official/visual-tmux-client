(() => {
 if (window.__vtcGeometryStarted) return;
 window.__vtcGeometryStarted = true;
 // No reliance on timers or animation frames: a covered window has both
 // suspended by the WebView. Every wait resolves from DOM mutations.
 const post = async body => {
  const response=await fetch('/api/desktop/fixture-geometry',{method:'POST',
   headers:{'Content-Type':'application/json',Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')},
   body:JSON.stringify(body)});
  // A refused snapshot stops the native side, which owns the comparison.
  if(!response.ok && !body.snapshot) throw new Error('Fixture request failed: '+body.phase);
 };
 const report = error => post({phase:'error',message:String(error?.message||error).slice(0,1024)}).catch(() => {});
 window.addEventListener('unhandledrejection', event => report(event.reason));
 window.addEventListener('error', event => report(event.error||event.message));
 const step = message => post({phase:'checkpoint',message}).catch(() => {});

 const waitFor = (predicate,label) => new Promise((resolve,reject) => {
  let done=false;
  const finish=(ok,error)=>{if(done)return;done=true;observer.disconnect();ok?resolve(true):reject(error);};
  const check=()=>{let ok=false;try{ok=!!predicate();}catch{}if(ok)finish(true);};
  const observer=new MutationObserver(check);
  observer.observe(document.documentElement,{subtree:true,childList:true,attributes:true,characterData:true});
  check();
  // Only the failure path uses a timer; a throttled timer merely delays it.
  setTimeout(()=>finish(false,new Error('Timed out: '+label)),30000);
 });
 const turns = async count => {for(let i=0;i<count;i++) await Promise.resolve();};
 // State that arrives over the network renders after the click's own update.
 // A throttled timer only makes this wait longer, never shorter.
 const delay = async ms => {await new Promise(r=>setTimeout(r,ms));await turns(20);};

 const canvas = document.createElement('canvas').getContext('2d');
 const measureText=(font,text)=>{canvas.font=font;return canvas.measureText(text).width;};
 const measure = scene => {
  const controls=[];
  // The key is the element's position among all controls, so it does not
  // depend on the label, which differs between languages.
  document.querySelectorAll('button, select, input, textarea').forEach((element,index) => {
   if(['checkbox','radio','hidden'].includes(element.type)) return;
   // Notifications come and go on their own schedule; they are not controls.
   if(element.closest('.toast')) return;
   const style=getComputedStyle(element);
   const rect=element.getBoundingClientRect();
   // Controls under an open dialog stay laid out; hidden ones do not count.
   if(style.display==='none'||style.visibility==='hidden'||rect.width<1||rect.height<1||element.offsetParent===null) return;
   const font=`${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
   const lineHeight=style.lineHeight==='normal'?parseFloat(style.fontSize)*1.5:parseFloat(style.lineHeight);
   let label=0,text='',wrapped=false;
   if(element.tagName==='SELECT'){
    for(const option of element.options) label=Math.max(label,measureText(font,option.textContent));
    text=element.options[element.selectedIndex]?.textContent||'';
   } else if(element.tagName==='INPUT'||element.tagName==='TEXTAREA'){
    text=element.value||element.placeholder||'';label=measureText(font,text);
   } else {
    const range=document.createRange();range.selectNodeContents(element);
    const bounds=range.getBoundingClientRect();label=bounds.width;
    wrapped=bounds.height>lineHeight*1.5;
    text=(element.innerText||'').trim();
   }
   const inner=element.clientWidth-parseFloat(style.paddingLeft)-parseFloat(style.paddingRight);
   controls.push({key:`${element.tagName.toLowerCase()}:${index}`,class:typeof element.className==='string'?element.className:'',
    text:text.slice(0,40),w:rect.width,h:rect.height,tw:label,
    overflow:wrapped||label>inner+1||element.scrollWidth>element.clientWidth+1});
  });
  return {scene,locale:document.documentElement.lang,controls};
 };
 const snapshot = async scene => {await delay(400);await post({phase:'snapshot',snapshot:measure(scene)});};

 // The real selector drives the application's own live language switch.
 const switchLanguage = async locale => {
  const select=document.querySelector('.app__lang-select');
  select.value=locale;select.dispatchEvent(new Event('change',{bubbles:true}));
  await waitFor(()=>document.documentElement.lang===locale&&select.value===locale,'language switch to '+locale);
  await delay(400);
 };
 const escape = () => window.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));

 (async()=>{
  try{
   await step('page state: '+document.visibilityState+' '+document.documentElement.lang);
   await waitFor(()=>document.querySelector('.app__bar .app__lang-select')&&
    document.querySelector('.session-list__row, .session-list__empty, .session-list__error'),'workspace');
   // Selecting the synthetic session opens the terminal header and its controls.
   await waitFor(()=>document.querySelector('.session-list__row'),'synthetic session row');
   document.querySelector('.session-list__row').click();
   await waitFor(()=>document.querySelectorAll('.app__term-btn').length>=4,'terminal header');
   await delay(600);
   await step('workspace rendered');
   // Both workspace snapshots are taken at the same age of the page, so a
   // connection state that changes over time cannot differ between them.
   await switchLanguage('en');
   await snapshot('workspace');
   await switchLanguage('zh-CN');
   await snapshot('workspace');
   await step('workspace measured in both languages');
   await switchLanguage('en');
   document.querySelectorAll('.app__bar-actions button')[1].click();
   await waitFor(()=>document.querySelector('.host-list-view .host-modal__add-btn')&&document.querySelector('.host-card'),'host manager list');
   await snapshot('hosts');
   await step('english host list measured');
   document.querySelector('.host-modal__add-btn').click();
   await waitFor(()=>document.querySelector('form.host-form'),'add-host form');
   await snapshot('add-host');
   await step('english add-host form measured');
   await switchLanguage('zh-CN');
   await snapshot('add-host');
   await step('chinese add-host form measured');
   escape();
   await waitFor(()=>!document.querySelector('form.host-form'),'return to host manager list');
   await snapshot('hosts');
   await step('chinese host list measured');
   escape();
   await waitFor(()=>!document.querySelector('.host-modal'),'host manager closed');
   await step('host manager closed');
  }catch(error){await report(error);}
 })();
})()
