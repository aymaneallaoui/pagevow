package browser

import (
	_ "embed"
)

//go:embed snapshot.js
var snapshotScript string

const guardScript = `(() => { const c=window.__jevFast; return c ? [c.pageKey(),c.guard(c.nodes.get(%d))] : null; })()`

const afterInputScript = `(action => new Promise(resolve => {
  const field=window.__jevFast?.nodes.get(action.node);
  const autocomplete=action.kind==='fill' && field?.getAttribute('role')==='combobox';
  let frames=0, stopped=false;
  const finish=()=>{stopped=true;resolve()};
  setTimeout(finish,autocomplete ? 200 : 50);
  const ready=()=>{
    if (stopped) return;
    const ids=(field?.getAttribute('aria-controls')||field?.getAttribute('aria-owns')||'')
      .split(/\s+/).filter(Boolean);
    const scope=field?.getRootNode() || document;
    const roots=ids.length ? ids.map(id=>scope.getElementById(id)).filter(Boolean) : [scope];
    const options=roots.flatMap(root=>[...root.querySelectorAll('[role="option"]')]);
    if (++frames>=2 && (!autocomplete || options.some(e=>{
      const r=e.getBoundingClientRect();
      return r.width && r.height && r.bottom>0 && r.top<innerHeight &&
        e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true});
    }))) finish();
    else requestAnimationFrame(ready);
  };
  requestAnimationFrame(ready);
}))`

const targetScript = `(action => {
  const e=window.__jevFast?.nodes.get(action.node);
  if (!e?.isConnected || e.matches(':disabled') ||
      window.__jevFast.closest(e,'[aria-disabled="true"],[inert]') ||
      !e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true})) return null;
  if (action.kind==='fill' && (e.readOnly || e.getAttribute('aria-readonly')==='true')) return null;
  e.scrollIntoView({block:'nearest',inline:'nearest',behavior:'instant'});
  const r=e.getBoundingClientRect(), x=r.x+r.width/2, y=r.y+r.height/2;
  if (!r.width || !r.height || x<0 || y<0 || x>=innerWidth || y>=innerHeight) return null;
  let hit=document.elementFromPoint(x,y);
  while (hit?.shadowRoot) {
    const inner=hit.shadowRoot.elementFromPoint(x,y);
    if (!inner || inner===hit) break;
    hit=inner;
  }
  let within=false;
  for (let n=hit; n && !within; n=n.parentNode || n.host) within=n===e;
  if (!within) return null;
  if (action.kind==='select') {
    if (e.tagName!=='SELECT' || ![...e.options].some(o=>o.value===action.value &&
        !o.disabled && !o.closest('optgroup[disabled]'))) return null;
    e.value=action.value;
    e.dispatchEvent(new Event('input',{bubbles:true}));
    e.dispatchEvent(new Event('change',{bubbles:true}));
  }
  if (action.kind==='enter') e.focus();
  return {x,y};
})`

func markerExpression() string {
	return "(() => { const state=" + snapshotScript + `; return state?.marker ?? null; })()`
}
