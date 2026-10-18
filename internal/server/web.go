package server

const webIndex = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>SubCoder</title>
<style>
  :root { color-scheme: dark; --bg:#0d1117; --panel:#161b22; --edge:#30363d; --fg:#e6edf3; --dim:#8b949e; --accent:#58a6ff; --ok:#3fb950; --err:#f85149; }
  * { box-sizing: border-box; }
  body { margin:0; font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; background:var(--bg); color:var(--fg); }
  header { display:flex; align-items:center; gap:12px; padding:12px 18px; border-bottom:1px solid var(--edge); background:var(--panel); position:sticky; top:0; }
  header strong { color:var(--accent); }
  header span { color:var(--dim); font-size:12px; }
  main { max-width:900px; margin:0 auto; padding:18px; padding-bottom:140px; }
  .msg { white-space:pre-wrap; word-wrap:break-word; margin:12px 0; padding:10px 12px; border-radius:8px; border:1px solid var(--edge); background:var(--panel); }
  .msg.user { border-color:var(--accent); }
  .msg.tool { color:var(--dim); font-size:12px; padding:6px 10px; }
  .msg.error { border-color:var(--err); color:var(--err); }
  .muted { color:var(--dim); }
  form { position:fixed; bottom:0; left:0; right:0; background:var(--bg); border-top:1px solid var(--edge); padding:12px 18px; }
  .composer { max-width:900px; margin:0 auto; display:flex; gap:8px; }
  textarea { flex:1; resize:none; height:64px; background:var(--panel); color:var(--fg); border:1px solid var(--edge); border-radius:8px; padding:10px; font:inherit; }
  button { background:var(--accent); color:#0d1117; border:0; border-radius:8px; padding:0 18px; font:inherit; font-weight:700; cursor:pointer; }
  button:disabled { opacity:.5; cursor:default; }
  .permission { position:fixed; bottom:100px; left:50%; transform:translateX(-50%); max-width:900px; width:calc(100% - 36px); background:var(--panel); border:1px solid var(--accent); border-radius:10px; padding:14px; display:none; }
  .permission button { margin-right:8px; padding:6px 12px; }
  .permission button.reject { background:var(--err); }
</style>
</head>
<body>
<header><strong>SubCoder</strong><span id="status">connecting...</span></header>
<main id="log"></main>
<div class="permission" id="permission">
  <div id="permission-title"></div>
  <p></p>
  <button data-reply="once">Allow once</button>
  <button data-reply="always">Always allow</button>
  <button class="reject" data-reply="reject">Reject</button>
</div>
<form id="form"><div class="composer"><textarea id="prompt" placeholder="Ask SubCoder... (enter to send, shift+enter for newline)"></textarea><button id="send">Send</button></div></form>
<script>
var sessionID = null;
var busy = false;
var assistantEl = null;
var lastText = {};
var permissionReq = null;
var log = document.getElementById('log');
var statusEl = document.getElementById('status');

function el(tag, cls, text) {
  var node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text) node.textContent = text;
  return node;
}
function addMessage(role, text) {
  var node = el('div', 'msg ' + role, text);
  log.appendChild(node);
  window.scrollTo(0, document.body.scrollHeight);
  return node;
}
function setStatus(text) { statusEl.textContent = text; }

async function ensureSession() {
  if (sessionID) return sessionID;
  var response = await fetch('/sessions', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({title: 'web session'})
  });
  if (!response.ok) throw new Error(await response.text());
  var session = await response.json();
  sessionID = session.id;
  return sessionID;
}

function handleEvent(event) {
  if (event.type === 'part.updated' && event.part && event.message && event.message.role === 'assistant') {
    var part = event.part;
    if (part.type === 'text') {
      if (!assistantEl) assistantEl = addMessage('assistant', '');
      assistantEl.textContent = part.text;
      lastText[part.id] = part.text;
    } else if (part.type === 'reasoning' && part.text) {
      addMessage('tool', 'thinking: ' + part.text);
    } else if (part.type === 'tool' && part.tool) {
      var line = 'tool ' + part.tool.name + ': ' + (part.tool.title || part.tool.state);
      addMessage('tool', line);
    }
  } else if (event.type === 'permission.asked' && event.data) {
    permissionReq = event.data;
    document.getElementById('permission-title').textContent = 'Permission: ' + event.data.title;
    document.getElementById('permission').style.display = 'block';
  } else if (event.type === 'session.error') {
    addMessage('error', event.error);
  }
}

async function send(text) {
  if (busy || !text.trim()) return;
  busy = true;
  assistantEl = null;
  lastText = {};
  addMessage('user', text);
  setStatus('working...');
  try {
    var id = await ensureSession();
    var response = await fetch('/sessions/' + id + '/messages', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({text: text})
    });
    if (!response.ok || !response.body) throw new Error(await response.text());
    var reader = response.body.getReader();
    var decoder = new TextDecoder();
    var buffer = '';
    while (true) {
      var chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, {stream: true});
      var lines = buffer.split('\n');
      buffer = lines.pop();
      for (var i = 0; i < lines.length; i++) {
        var line = lines[i].trim();
        if (!line.startsWith('data:')) continue;
        var data = line.slice(5).trim();
        if (!data) continue;
        try { handleEvent(JSON.parse(data)); } catch (err) { console.error(err); }
      }
    }
  } catch (err) {
    addMessage('error', String(err));
  } finally {
    busy = false;
    assistantEl = null;
    setStatus('ready');
  }
}

document.getElementById('form').addEventListener('submit', function(event) {
  event.preventDefault();
  var input = document.getElementById('prompt');
  var text = input.value;
  input.value = '';
  send(text);
});
document.getElementById('prompt').addEventListener('keydown', function(event) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    document.getElementById('form').requestSubmit();
  }
});
document.getElementById('permission').addEventListener('click', async function(event) {
  var reply = event.target.getAttribute('data-reply');
  if (!reply || !permissionReq) return;
  document.getElementById('permission').style.display = 'none';
  await fetch('/sessions/' + sessionID + '/permissions/' + permissionReq.ID, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({reply: reply})
  });
  permissionReq = null;
});

ensureSession().then(function() { setStatus('ready'); }).catch(function(err) { setStatus(String(err)); });
</script>
</body>
</html>
`
