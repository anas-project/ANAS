'use strict';
// Loaded synchronously before the stylesheet paints so the last theme used in
// this browser applies at once. The account preference replaces it after sign-in.
(() => {
  let theme='system';
  try{const saved=localStorage.getItem('anas.portal.theme');if(['system','light','dark'].includes(saved))theme=saved;}catch{}
  document.documentElement.dataset.theme=theme;
})();
