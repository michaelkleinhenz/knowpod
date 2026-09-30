// Scrollbars that take up room (Windows, Linux, the desktop app there) get the app's own
// look: styles.css reserves their space on <html> so pages don't shift when they start to
// scroll, and that space, left empty, would cut the header off short of the window's right
// edge. With data-scrollbars="classic" on <html>, styles.css draws the scrollbar itself and
// paints its top in the header's colors (the header's height is set by Layout.tsx).
// Overlay scrollbars (macOS, phones) take no room and are left alone.

// classicScrollbars says whether scrollbars take up room: a scrolling box's content is then
// narrower than the box.
function classicScrollbars() {
  const probe = document.createElement('div');
  probe.style.cssText = 'position:absolute;top:-999px;width:100px;height:100px;overflow-y:scroll;visibility:hidden';
  document.body.appendChild(probe);
  const classic = probe.offsetWidth - probe.clientWidth > 0;
  probe.remove();
  return classic;
}

if (classicScrollbars()) document.documentElement.dataset.scrollbars = 'classic';
