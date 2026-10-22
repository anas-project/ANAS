'use strict';
// Loaded synchronously before the stylesheet paints so the last theme used in
// this browser applies at once. The account preference replaces it after sign-in.
(() => {
  let theme='system';
  try{const saved=localStorage.getItem('anas.portal.theme');if(['system','light','dark'].includes(saved))theme=saved;}catch{}
  document.documentElement.dataset.theme=theme;
  // Only cached, validated color values are applied through CSSOM before paint.
  try {
    const colors=JSON.parse(localStorage.getItem('anas.portal.themeColors')||'{}');
    const dark=theme==='dark'||theme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches;
    const palette=colors[dark?'dark':'light']||{};
    const tokens=['bg','surface','surface-2','text','text-2','text-3','line','line-strong','primary','on-primary','accent-soft','accent-text','ok-bg','ok','warn-bg','warn-line','warn','danger-bg','danger-line','danger','sidebar','sidebar-text','sidebar-active','focus'];
    for(const key of tokens)if(/^#[0-9a-fA-F]{6}$/.test(palette[key]||''))document.documentElement.style.setProperty('--'+key,palette[key]);
  } catch {}
})();
