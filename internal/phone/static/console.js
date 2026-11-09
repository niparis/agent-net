// Keys and small toggles for the console. Everything else is server-rendered
// HTML with htmx and the SSE extension.
(function () {
  var ROUTES = { '1': '/', '2': '/sessions', '3': '/graph', '4': '/changes', '5': '/messages', '6': '/workers' };
  var app = document.getElementById('app');
  var cto = document.getElementById('cto');
  var toggle = document.getElementById('toggleCTO');

  // The panel has its own section of /events, so a screen and the panel update
  // independently. The composer is inside the section and is not redrawn while
  // the owner writes in it.
  var ctoStream = null;
  function ctoToBottom(offset) {
    var log = document.getElementById('ctolog');
    if (log) log.scrollTop = log.scrollHeight - log.clientHeight - (offset || 0);
  }
  function startCTOStream() {
    if (ctoStream || !cto || !window.EventSource) return;
    var url = cto.dataset.events;
    if (!url) return;
    ctoStream = new EventSource(url);
    ctoStream.addEventListener('cto', function (e) {
      var body = document.getElementById('ctobody');
      if (!body) return;
      var a = document.activeElement;
      if (a && cto.contains(a) && /^(INPUT|TEXTAREA)$/.test(a.tagName)) return;
      var field = body.querySelector('textarea');
      if (field && field.value) return;
      var log = document.getElementById('ctolog');
      var fromBottom = log ? log.scrollHeight - log.clientHeight - log.scrollTop : null;
      body.innerHTML = e.data;
      if (fromBottom !== null) ctoToBottom(fromBottom > 8 ? fromBottom : 0);
    });
  }
  function stopCTOStream() {
    if (ctoStream) { ctoStream.close(); ctoStream = null; }
  }
  function setCTO(open) {
    if (!cto || !app) return;
    cto.hidden = !open;
    app.classList.toggle('cto-open', open);
    if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    try { localStorage.setItem('anw-cto', open ? '1' : '0'); } catch (e) {}
    document.cookie = 'anw-cto=' + (open ? '1' : '0') + '; path=/; SameSite=Lax';
    if (open) { startCTOStream(); ctoToBottom(0); } else stopCTOStream();
  }
  if (toggle) toggle.addEventListener('click', function () { setCTO(cto.hidden); });
  // The panel header is redrawn by the stream, so the close button is found
  // when it is clicked.
  document.addEventListener('click', function (e) {
    if (e.target.closest && e.target.closest('#closeCTO')) setCTO(false);
  });
  var storedCTO = null;
  try { storedCTO = localStorage.getItem('anw-cto'); } catch (e) {}
  if (storedCTO === '1') {
    setCTO(true);
  } else if (storedCTO === '0') {
    setCTO(false);
  } else if (cto && !cto.hidden) {
    startCTOStream();
    ctoToBottom(0);
  }

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
