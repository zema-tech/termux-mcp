package oauth

// Consent and landing page HTML (inlined — no go:embed, works on all Go versions).

const consentHTML = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="dark">
<title>Authorize · termux-mcp</title>
<style>
:root{
  --bg:#0b0d12;--card:#141820;--card-border:#242a36;--text:#eef0f4;--muted:#9aa3b2;
  --accent:#4c8dff;--accent-2:#6ee7b7;--danger:#3a3f4a;--radius:18px;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  background:
    radial-gradient(1200px 600px at 10% -10%, rgba(76,141,255,.18), transparent 55%),
    radial-gradient(900px 500px at 100% 0%, rgba(110,231,183,.10), transparent 50%),
    var(--bg);
  color:var(--text);display:flex;align-items:center;justify-content:center;
  padding:max(1.25rem, env(safe-area-inset-top)) 1.25rem max(1.25rem, env(safe-area-inset-bottom));
  min-height:100%;
}
.shell{width:100%;max-width:26rem}
.brand{display:flex;align-items:center;gap:.65rem;justify-content:center;margin-bottom:1.25rem;color:var(--muted);font-size:.85rem}
.logo{width:2rem;height:2rem;border-radius:10px;background:linear-gradient(145deg,#4c8dff,#6ee7b7);display:grid;place-items:center;color:#0b0d12;font-weight:800;box-shadow:0 8px 24px rgba(76,141,255,.25)}
.card{background:linear-gradient(180deg, rgba(255,255,255,.03), transparent 40%), var(--card);border:1px solid var(--card-border);border-radius:var(--radius);padding:1.5rem 1.4rem 1.35rem;box-shadow:0 20px 50px rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.04)}
.app-row{display:flex;align-items:center;gap:.9rem;margin-bottom:1rem}
.app-logo,.app-avatar{width:3.5rem;height:3.5rem;border-radius:14px;object-fit:cover;background:#1c2230;border:1px solid var(--card-border);flex:0 0 auto}
.app-avatar{display:grid;place-items:center;font-size:1.35rem;font-weight:700;color:var(--accent);background:linear-gradient(145deg,rgba(76,141,255,.2),rgba(110,231,183,.12))}
.app-meta{min-width:0}
.app-meta .name{font-weight:700;font-size:1.05rem;letter-spacing:-.01em;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.app-meta .sub{color:var(--muted);font-size:.8rem;margin-top:.15rem}
.badge{display:inline-flex;font-size:.72rem;font-weight:600;letter-spacing:.04em;text-transform:uppercase;color:var(--accent-2);background:rgba(110,231,183,.1);border:1px solid rgba(110,231,183,.25);border-radius:999px;padding:.28rem .65rem;margin-bottom:1rem}
h1{font-size:1.35rem;line-height:1.25;margin:0 0 .75rem;font-weight:700;letter-spacing:-.02em}
.lead{margin:0 0 1.15rem;color:var(--muted);line-height:1.55;font-size:.95rem}
.client{font-weight:650;background:linear-gradient(90deg,#8ab4f8,#6ee7b7);-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent}
.scopes{list-style:none;margin:0 0 1.15rem;padding:0;display:grid;gap:.55rem}
.scopes li{display:flex;align-items:flex-start;gap:.7rem;background:rgba(255,255,255,.03);border:1px solid var(--card-border);border-radius:12px;padding:.7rem .8rem;font-size:.88rem;line-height:1.4}
.scopes .ico{flex:0 0 auto;width:1.55rem;height:1.55rem;border-radius:8px;display:grid;place-items:center;font-size:.85rem;background:rgba(76,141,255,.12);color:var(--accent)}
.scopes strong{display:block;font-weight:600;color:var(--text);margin-bottom:.1rem}
.scopes span{color:var(--muted);font-size:.8rem}
.note{margin:0 0 1.1rem;font-size:.78rem;color:var(--muted);line-height:1.45;padding:.65rem .75rem;border-radius:10px;background:rgba(255,255,255,.025);border:1px dashed var(--card-border)}
.token-box{margin:0 0 1.15rem;padding:.85rem .9rem;border-radius:12px;border:1px solid var(--card-border);background:rgba(76,141,255,.06)}
.token-title{font-size:.82rem;font-weight:650;margin-bottom:.35rem}
.token-help{margin:0 0 .65rem;font-size:.75rem;color:var(--muted);line-height:1.45}
.token-input{width:100%;padding:.7rem .75rem;border-radius:10px;border:1px solid #2f3645;background:#0f131a;color:var(--text);font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.85rem}
.token-input:focus{outline:2px solid rgba(76,141,255,.45);border-color:var(--accent)}
.actions{display:flex;gap:.65rem}
button{flex:1;padding:.85rem 1rem;border:none;border-radius:12px;font-size:.95rem;font-weight:650;cursor:pointer;transition:transform .12s ease,filter .12s ease;-webkit-tap-highlight-color:transparent}
button:active{transform:scale(.98)}
.approve{background:linear-gradient(180deg,#5b97ff,#3b7aef);color:#fff;box-shadow:0 8px 20px rgba(59,122,239,.35)}
.approve:hover{filter:brightness(1.05)}
.deny{background:var(--danger);color:var(--text);border:1px solid #4a5160}
.deny:hover{background:#454b57}
.foot{margin-top:1rem;text-align:center;font-size:.72rem;color:#6b7280}
code{font-size:.85em;color:#8ab4f8}
</style>
</head><body>
<div class="shell">
  <div class="brand"><div class="logo" aria-hidden="true">⌁</div><span>termux-mcp</span></div>
  <div class="card">
    <div class="badge">OAuth 2.1 · PKCE</div>
    <div class="app-row">
      %s
      <div class="app-meta">
        <div class="name">%s</div>
        <div class="sub">chiede accesso a questo dispositivo</div>
      </div>
    </div>
    <h1>Authorize access</h1>
    <p class="lead"><span class="client">%s</span> vuole usare i tool su questo Android tramite <strong>termux-mcp</strong>.</p>
    <ul class="scopes">
      <li><span class="ico" aria-hidden="true">📱</span><div><strong>Device tools</strong><span>Battery, sensor, location, clipboard, system info</span></div></li>
      <li><span class="ico" aria-hidden="true">📁</span><div><strong>Files</strong><span>Accesso sandbox — solo se abilitato in config</span></div></li>
      <li><span class="ico" aria-hidden="true">⌨</span><div><strong>Shell</strong><span>Comandi — solo se allowlist attiva</span></div></li>
    </ul>
    <p class="note">Sensitive e dangerous restano spenti finché non li abiliti tu in config. Dopo Approve il client riceve un access token Bearer (24h).</p>
    <form method="POST" action="/oauth/authorize">
      <input type="hidden" name="client_id" value="%s">
      <input type="hidden" name="redirect_uri" value="%s">
      <input type="hidden" name="state" value="%s">
      <input type="hidden" name="code_challenge" value="%s">
      <input type="hidden" name="code_challenge_method" value="%s">
      <input type="hidden" name="scope" value="%s">
      <div class="token-box">
        <div class="token-title">Bearer token (opzionale)</div>
        <p class="token-help">Se hai un <code>auth.token</code> statico, incollalo qui per confermare. L&rsquo;agente approvato userà l&rsquo;access token OAuth; il bearer statico resta valido in parallelo per i client che lo inviano come <code>Authorization: Bearer</code>.</p>
        <input class="token-input" type="password" name="bearer_token" placeholder="Incolla bearer token…" autocomplete="off" spellcheck="false">
      </div>
      <div class="actions">
        <button class="deny" type="submit" name="decision" value="deny">Deny</button>
        <button class="approve" type="submit" name="decision" value="approve">Approve</button>
      </div>
    </form>
  </div>
  <p class="foot">Richiesta da %s · token OAuth valido 24h</p>
</div>
</body></html>
`

const landingHTML = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>termux-mcp · OAuth</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font-family:ui-sans-serif,system-ui,sans-serif;background:#0b0d12;color:#eef0f4;padding:1.5rem;background-image:radial-gradient(800px 400px at 20% 0%,rgba(76,141,255,.15),transparent 55%)}
.card{max-width:26rem;background:#141820;border:1px solid #242a36;border-radius:18px;padding:1.5rem;box-shadow:0 20px 50px rgba(0,0,0,.4)}
h1{font-size:1.25rem;margin:0 0 .75rem}
p{color:#9aa3b2;line-height:1.55;margin:0 0 .75rem}
code{color:#8ab4f8;font-size:.9em}
.logo{width:2.25rem;height:2.25rem;border-radius:10px;margin-bottom:1rem;background:linear-gradient(145deg,#4c8dff,#6ee7b7);display:grid;place-items:center;color:#0b0d12;font-weight:800}
</style></head><body>
<div class="card">
<div class="logo" aria-hidden="true">⌁</div>
<h1>termux-mcp OAuth</h1>
<p>Dispositivo pronto per client OAuth 2.1 + PKCE (Grok, Claude, Composio, …).</p>
<p>Discovery: <code>/.well-known/oauth-protected-resource</code></p>
<p>I Bearer token statici (<code>auth.token</code>) restano validi insieme ai token OAuth.</p>
</div>
</body></html>
`
