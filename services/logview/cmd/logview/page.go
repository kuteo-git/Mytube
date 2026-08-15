package main

// The page, in one string.
//
// No build step and no assets: this has to work when the rest of the stack does
// not, including when Vite is not running, and a log viewer that needs npm to
// come up is a log viewer that is unavailable on exactly the afternoons it is
// wanted.
//
// Filtering happens in the browser rather than on the server. Every line is
// already being sent for the live view, so filtering here as well would mean
// two implementations of the same predicate that agree until the first time one
// is fixed — and it would make changing a filter a round trip instead of
// instant.
const page = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>logs</title>
<style>
  :root {
    --bg: #0f1115; --panel: #171a21; --line: #262b36;
    --text: #d7dbe3; --dim: #7d8697;
    --info: #6ea8fe; --warn: #e2b341; --error: #ef6a6a; --debug: #8f7fd6;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text);
    font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  header {
    position: sticky; top: 0; z-index: 2; background: var(--panel);
    border-bottom: 1px solid var(--line); padding: 8px 12px;
    display: flex; flex-wrap: wrap; gap: 8px 14px; align-items: center;
  }
  .group { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  label { display: flex; gap: 5px; align-items: center; cursor: pointer; white-space: nowrap; }
  input[type=search] {
    background: var(--bg); border: 1px solid var(--line); color: var(--text);
    padding: 5px 9px; border-radius: 6px; min-width: 220px; font: inherit;
  }
  input[type=search]:focus { outline: 2px solid var(--info); outline-offset: 1px; }
  select { background: var(--bg); border: 1px solid var(--line); color: var(--text);
           padding: 5px 7px; border-radius: 6px; font: inherit; }
  .count { color: var(--dim); margin-left: auto; }
  main { padding: 6px 0 40px; }
  .row {
    display: grid; grid-template-columns: 88px 74px 62px 1fr; gap: 10px;
    padding: 1px 12px; white-space: pre-wrap; word-break: break-word;
    border-left: 3px solid transparent;
  }
  .row:hover { background: #ffffff08; }
  .row.ERROR { border-left-color: var(--error); background: #ef6a6a10; }
  .row.WARN  { border-left-color: var(--warn); }
  .at, .svc, .lvl { color: var(--dim); }
  .lvl.ERROR { color: var(--error); } .lvl.WARN { color: var(--warn); }
  .lvl.INFO { color: var(--info); } .lvl.DEBUG { color: var(--debug); }
  .svc { color: #9aa4b5; }
  .restart {
    margin: 10px 12px; padding: 3px 10px; border-top: 1px dashed var(--line);
    color: var(--dim); font-size: 12px;
  }
  mark { background: #e2b34155; color: inherit; border-radius: 2px; }
  .empty { color: var(--dim); padding: 24px 12px; }
  @media (max-width: 700px) {
    .row { grid-template-columns: 62px 1fr; }
    .svc, .lvl { display: none; }
  }
</style>

<header>
  <div class="group" id="services"></div>
  <div class="group">
    <select id="level">
      <option value="">all levels</option>
      <option value="ERROR">error</option>
      <option value="WARN">warn and worse</option>
      <option value="INFO">info and worse</option>
    </select>
    <input type="search" id="q" placeholder="search text…" autocomplete="off">
    <label><input type="checkbox" id="errorsOnly"> errors only</label>
    <label><input type="checkbox" id="follow" checked> follow</label>
  </div>
  <span class="count" id="count"></span>
</header>
<main id="out"></main>

<script>
(function () {
  var lines = [];            // everything received, unfiltered
  var services = new Set();  // which are ticked
  var out = document.getElementById('out');
  var follow = document.getElementById('follow');

  // Worse-or-equal, so "warn" shows errors too. A level filter that hid the
  // errors while showing the warnings would be a trap rather than a filter.
  var RANK = { DEBUG: 0, INFO: 1, WARN: 2, WARNING: 2, ERROR: 3, FATAL: 4, CRITICAL: 4 };

  function esc(s) {
    return s.replace(/[&<>]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c];
    });
  }

  function highlight(text, needle) {
    var safe = esc(text);
    if (!needle) return safe;
    var at = safe.toLowerCase().indexOf(esc(needle).toLowerCase());
    if (at < 0) return safe;
    var n = esc(needle).length;
    return safe.slice(0, at) + '<mark>' + safe.slice(at, at + n) + '</mark>' + safe.slice(at + n);
  }

  function clock(iso) {
    if (!iso || iso.startsWith('0001')) return '';
    var d = new Date(iso);
    if (isNaN(d)) return '';
    return d.toTimeString().slice(0, 8);
  }

  function keep(line) {
    if (line.restart) return services.size === 0 || services.has(line.service);
    if (services.size && !services.has(line.service)) return false;

    var wanted = document.getElementById('level').value;
    if (document.getElementById('errorsOnly').checked) wanted = 'ERROR';
    if (wanted) {
      // A line with no level of its own is kept whenever anything below error
      // is being asked for: a panic trace has no level and is not noise.
      var rank = RANK[line.level];
      if (rank === undefined) { if (wanted === 'ERROR') return false; }
      else if (rank < RANK[wanted]) return false;
    }

    var q = document.getElementById('q').value.trim();
    return !q || line.raw.toLowerCase().indexOf(q.toLowerCase()) >= 0;
  }

  function render(line) {
    var q = document.getElementById('q').value.trim();
    if (line.restart) {
      var hr = document.createElement('div');
      hr.className = 'restart';
      hr.textContent = '— stack restarted ' + (clock(line.at) || '') + ' —';
      return hr;
    }
    var row = document.createElement('div');
    row.className = 'row ' + (line.level || '');
    row.innerHTML =
      '<span class="at">' + clock(line.at) + '</span>' +
      '<span class="svc">' + esc(line.service) + '</span>' +
      '<span class="lvl ' + (line.level || '') + '">' + esc(line.level || '') + '</span>' +
      '<span class="msg">' + highlight(line.raw, q) + '</span>';
    return row;
  }

  function atBottom() {
    return window.innerHeight + window.scrollY >= document.body.offsetHeight - 60;
  }

  function draw() {
    var shown = 0;
    var frag = document.createDocumentFragment();
    for (var i = 0; i < lines.length; i++) {
      if (!keep(lines[i])) continue;
      frag.appendChild(render(lines[i]));
      shown++;
    }
    out.innerHTML = '';
    if (!shown) {
      var p = document.createElement('p');
      p.className = 'empty';
      p.textContent = lines.length ? 'nothing matches those filters.' : 'no log lines yet.';
      out.appendChild(p);
    }
    out.appendChild(frag);
    document.getElementById('count').textContent = shown + ' of ' + lines.length + ' lines';
    if (follow.checked) window.scrollTo(0, document.body.scrollHeight);
  }

  function append(line) {
    lines.push(line);
    // Bounded, or a page left open overnight ends up holding the afternoon in
    // the DOM and stops scrolling smoothly.
    if (lines.length > 20000) lines = lines.slice(-15000);
    if (!keep(line)) {
      document.getElementById('count').textContent =
        document.querySelectorAll('.row,.restart').length + ' of ' + lines.length + ' lines';
      return;
    }
    var stick = follow.checked && atBottom();
    if (out.querySelector('.empty')) out.innerHTML = '';
    out.appendChild(render(line));
    document.getElementById('count').textContent =
      document.querySelectorAll('.row,.restart').length + ' of ' + lines.length + ' lines';
    if (stick) window.scrollTo(0, document.body.scrollHeight);
  }

  function buildServiceBoxes(names) {
    var host = document.getElementById('services');
    host.innerHTML = '';
    names.forEach(function (name) {
      var label = document.createElement('label');
      var box = document.createElement('input');
      box.type = 'checkbox';
      box.checked = true;
      services.add(name);
      box.addEventListener('change', function () {
        if (box.checked) services.add(name); else services.delete(name);
        draw();
      });
      label.appendChild(box);
      label.appendChild(document.createTextNode(name));
      host.appendChild(label);
    });
  }

  ['level', 'q', 'errorsOnly'].forEach(function (id) {
    document.getElementById(id).addEventListener('input', draw);
    document.getElementById(id).addEventListener('change', draw);
  });

  var known = {};

  fetch('backlog').then(function (r) { return r.json(); }).then(function (data) {
    (data.services || []).forEach(function (n) { known[n] = true; });
    buildServiceBoxes(Object.keys(known).sort());
    lines = data.lines || [];
    draw();

    var stream = new EventSource('stream');
    stream.onmessage = function (event) {
      var line = JSON.parse(event.data);
      // A service started after this page was opened brings a log file with
      // it, and without a box of its own its lines would be filtered out by a
      // set it was never added to.
      if (!known[line.service]) {
        known[line.service] = true;
        buildServiceBoxes(Object.keys(known).sort());
      }
      append(line);
    };
  });
})();
</script>
`
