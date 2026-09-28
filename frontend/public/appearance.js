// Applies the color scheme and text size last used on this device before the page is drawn,
// so a dark app doesn't flash light while it starts (src/lib/appearance.ts and
// src/lib/fontSize.ts keep them up to date).
(function () {
  var pref = '';
  try {
    pref = localStorage.getItem('knowpod.appearance') || '';
  } catch (e) {
    // storage unavailable
  }
  var dark = pref === 'dark' || (pref !== 'light' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';

  var size = '';
  try {
    size = localStorage.getItem('knowpod.fontSize') || '';
  } catch (e) {
    // storage unavailable
  }
  if (/^(xsmall|small|large|xlarge)$/.test(size)) document.documentElement.dataset.fontSize = size;
})();
