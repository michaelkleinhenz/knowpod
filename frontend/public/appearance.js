// Applies the color scheme last used on this device before the page is drawn, so a dark
// app doesn't flash light while it starts (src/lib/appearance.ts keeps it up to date).
(function () {
  var pref = '';
  try {
    pref = localStorage.getItem('knowpod.appearance') || '';
  } catch (e) {
    // storage unavailable
  }
  var dark = pref === 'dark' || (pref !== 'light' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
})();
