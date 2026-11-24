// Actualización en tempo real da web local de Syncply.
//
// A páxina indica no elemento #vivo que tipos de cambio lle interesan
// (data-on) e, opcionalmente, cada cantos segundos recargar igualmente
// (data-every). Cando chega un aviso por /events, pídese de novo o HTML
// da páxina e substitúense as rexións ([data-rexion]) que cambiaron, agás
// as que o usuario está a usar (foco, despregable aberto ou campo editado).
// Sen JavaScript, a páxina funciona igual (ligazóns «Actualizar»).
(function () {
  'use strict';
  if (!window.EventSource || !window.fetch || !window.DOMParser) return;
  if (!document.getElementById('vivo')) return;

  var es = null, busy = false, again = false, stopped = false, last = Date.now();
  var skipped = false; // algunha rexión quedou sen actualizar por estar en uso

  function config() {
    var v = document.getElementById('vivo');
    return {
      on: v ? (v.getAttribute('data-on') || '').split(' ') : [],
      every: v ? parseInt(v.getAttribute('data-every') || '0', 10) : 0
    };
  }

  // O usuario está a usar esta rexión: non tocala.
  function inUse(el) {
    var a = document.activeElement;
    if (a && a !== document.body && el.contains(a)) return true;
    if (el.querySelector('details[open]')) return true;
    var f = el.querySelectorAll('input, textarea');
    for (var i = 0; i < f.length; i++) {
      if (f[i].type !== 'hidden' && f[i].value !== f[i].defaultValue) return true;
    }
    return false;
  }

  function regions(root) {
    var out = {}, keys = [];
    var r = root.querySelectorAll('[data-rexion]');
    for (var i = 0; i < r.length; i++) {
      var k = r[i].getAttribute('data-rexion');
      out[k] = r[i];
      keys.push(k);
    }
    return { map: out, keys: keys.join(' ') };
  }

  function apply(html) {
    var doc = new DOMParser().parseFromString(html, 'text/html');
    var cur = document.querySelector('main'), nue = doc.querySelector('main');
    if (!cur || !nue) return;
    var a = regions(cur), b = regions(nue);
    skipped = false;
    if (a.keys === b.keys) {
      for (var k in b.map) {
        var old = a.map[k];
        if (old.innerHTML === b.map[k].innerHTML) continue;
        if (inUse(old)) skipped = true;
        else old.replaceWith(document.importNode(b.map[k], true));
      }
    } else if (inUse(cur)) {
      skipped = true;
    } else {
      // Cambiou o tipo de páxina (p. ex. detida → en marcha): todo menos
      // as mensaxes da acción anterior.
      var keep = cur.querySelectorAll(':scope > .msg');
      cur.replaceChildren.apply(cur, Array.prototype.slice.call(keep));
      var n = nue.children;
      for (var i = 0; i < n.length; i++) {
        if (!n[i].classList.contains('msg')) cur.appendChild(document.importNode(n[i], true));
      }
    }
    manual(!!es);
  }

  function refresh() {
    if (stopped) return;
    if (busy) { again = true; return; }
    busy = true;
    last = Date.now();
    fetch(location.pathname, { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) {
        if (r.status === 401) { stop(); return null; } // sesión caducada
        return r.ok ? r.text() : null;
      })
      .then(function (html) { if (html) apply(html); })
      .catch(function () {})
      .then(function () {
        busy = false;
        if (again) { again = false; refresh(); }
      });
  }

  // As ligazóns «Actualizar» só se ven se non hai actualización automática.
  function manual(live) {
    var l = document.querySelectorAll('[data-sen-vivo]');
    for (var i = 0; i < l.length; i++) l[i].hidden = live;
  }

  function open() {
    if (es || stopped) return;
    var first = true;
    es = new EventSource('/events');
    es.onopen = function () {
      manual(true);
      if (!first) refresh(); // perdéronse avisos mentres estaba cortado
      first = false;
    };
    es.onerror = function () { manual(false); };
    es.addEventListener('cambio', function (e) {
      var on = config().on, kinds = (e.data || '').split(' ');
      for (var i = 0; i < kinds.length; i++) {
        if (on.indexOf(kinds[i]) >= 0) { refresh(); return; }
      }
    });
  }

  function close() {
    if (es) { es.close(); es = null; }
  }

  function stop() {
    stopped = true;
    close();
    manual(false);
  }

  // Ao deixar de usar unha rexión que quedou sen actualizar, poñela ao día.
  var later = null;
  function catchUp() {
    if (!skipped) return;
    clearTimeout(later);
    later = setTimeout(function () { if (skipped) refresh(); }, 300);
  }
  document.addEventListener('focusout', catchUp);
  document.addEventListener('toggle', catchUp, true); // <details> pechado

  // Cada lapela aberta ten unha conexión: pechala cando non se ve (o
  // navegador limita as conexións por servidor) e poñerse ao día ao volver.
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) { close(); return; }
    open();
    refresh();
  });

  setInterval(function () {
    var s = config().every;
    if (s > 0 && !document.hidden && Date.now() - last >= s * 1000) refresh();
  }, 5000);

  if (!document.hidden) open();
})();
