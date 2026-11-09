// Keys and small toggles for the console. Everything else is server-rendered
// HTML with htmx and the SSE extension.
(function () {
  var ROUTES = { '1': '/', '2': '/sessions', '3': '/graph', '4': '/changes', '5': '/messages', '6': '/workers' };
  var app = document.getElementById('app');
  var cto = document.getElementById('cto');
  var toggle = document.getElementById('toggleCTO');

  function setCTO(open) {
    if (!cto || !app) return;
    cto.hidden = !open;
    app.classList.toggle('cto-open', open);
    if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    try { localStorage.setItem('anw-cto', open ? '1' : '0'); } catch (e) {}
  }
  if (toggle) toggle.addEventListener('click', function () { setCTO(cto.hidden); });
  var close = document.getElementById('closeCTO');
  if (close) close.addEventListener('click', function () { setCTO(false); });
  try { if (localStorage.getItem('anw-cto') === '1') setCTO(true); } catch (e) {}

  document.addEventListener('keydown', function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    var t = e.target;
    if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    if (ROUTES[e.key]) {
      if (location.pathname !== ROUTES[e.key]) location.href = ROUTES[e.key];
    } else if (e.key === 'c') {
      setCTO(cto.hidden);
    } else if (e.key === 'Escape') {
      setCTO(false);
    }
  });

  // A table row opens its session. The name inside it is a real link.
  document.addEventListener('click', function (e) {
    var row = e.target.closest && e.target.closest('tr[data-href]');
    if (row && !e.target.closest('a')) location.href = row.getAttribute('data-href');
  });

  // The thread of a conversation reads like a chat: it opens at the newest
  // message, and a live update keeps the place the owner scrolled to.
  var threadFromBottom = null;
  function toBottom(offset) {
    var th = document.getElementById('thread');
    if (th) th.scrollTop = th.scrollHeight - th.clientHeight - (offset || 0);
  }
  toBottom(0);
  document.addEventListener('htmx:sseBeforeMessage', function () {
    var th = document.getElementById('thread');
    threadFromBottom = th ? th.scrollHeight - th.clientHeight - th.scrollTop : null;
  });
  document.addEventListener('htmx:afterSwap', function () {
    if (threadFromBottom !== null) toBottom(threadFromBottom);
  });

  // A live update must not wipe what the owner is writing: skip it while a
  // field has focus, holds text, or an answer form is open.
  document.addEventListener('htmx:sseBeforeMessage', function (e) {
    var screen = document.getElementById('screen');
    if (!screen) return;
    var a = document.activeElement;
    if (a && screen.contains(a) && /^(INPUT|TEXTAREA)$/.test(a.tagName)) return e.preventDefault();
    if (screen.querySelector('details.write[open]')) return e.preventDefault();
    var fields = screen.querySelectorAll('textarea');
    for (var i = 0; i < fields.length; i++) if (fields[i].value) return e.preventDefault();
  });
})();
