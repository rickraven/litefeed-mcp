// Applies the theme saved by app.js ("light" or "dark") as early as
// possible. Without a saved value the system preference is used via CSS.
(function () {
  try {
    var t = localStorage.getItem('theme');
    if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t;
  } catch (e) { /* storage may be unavailable (private mode) */ }
})();
