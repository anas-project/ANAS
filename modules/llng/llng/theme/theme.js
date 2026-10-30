/* Native forms, CSRF fields and protocol redirects remain owned by LLNG. */
(() => {
  'use strict';
  const key = 'anas.portal.theme';
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  let choice = 'system';
  try { choice = localStorage.getItem(key) || choice; } catch (_) {}
  const requested = new URLSearchParams(location.search).get('theme');
  if (requested === 'dark' || requested === 'default') {
    choice = requested === 'dark' ? 'dark' : 'light';
    try { localStorage.setItem(key, choice); } catch (_) {}
  }
  const apply = () => {
    document.documentElement.dataset.anasTheme = choice === 'dark' || (choice !== 'light' && media.matches) ? 'dark' : 'light';
  };
  apply();
  media.addEventListener('change', apply);
  document.addEventListener('DOMContentLoaded', () => {
    const header = document.getElementById('header');
    if (!header) return;
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'anas-theme-toggle';
    const label = () => {
      const zh = (window.currentLanguage || document.documentElement.lang || navigator.language).toLowerCase().startsWith('zh');
      const labels = zh ? {system: '跟随系统', light: '浅色', dark: '深色'} : {system: 'System', light: 'Light', dark: 'Dark'};
      button.textContent = (zh ? '外观：' : 'Appearance: ') + (labels[choice] || labels.system);
      button.setAttribute('aria-label', button.textContent);
      const password = document.querySelector('a[href="#id_1_Password"]');
      const passkey = document.querySelector('a[href="#id_2_Passkey"]');
      if (password) password.textContent = zh ? '密码' : 'Password';
      if (passkey) passkey.textContent = zh ? '通行密钥' : 'Passkey';
    };
    button.addEventListener('click', () => {
      const order = ['system', 'light', 'dark']; choice = order[(order.indexOf(choice) + 1) % order.length];
      try { localStorage.setItem(key, choice); } catch (_) {}
      apply(); label();
    });
    const name = document.createElement('div'); name.className = 'anas-brand-name'; name.textContent = 'ANAS';
    label(); header.append(name, button);
    new MutationObserver(label).observe(document.documentElement, {attributes: true, attributeFilter: ['lang']});
    // Native language switching translates in place without changing html.lang.
    window.jQuery?.(document).ajaxComplete((_event, _response, settings) => {
      if (settings.url.includes('/languages/')) label();
    });
  });
})();
