const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
const $ = selector => document.querySelector(selector);
const appURL = path => new URL(path.replace(/^\//, ''), document.baseURI).toString();
let toastTimer;
let passwordChangeRequired = false;

function toast(message) {
  const el = $('#toast');
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 3500);
}

async function api(path, options = {}) {
  const headers = {'X-CSRF-Token': csrf, ...(options.headers || {})};
  if (options.body) headers['Content-Type'] = 'application/json';
  const response = await fetch(appURL(path), {...options, headers, credentials: 'same-origin'});
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

function openPasswordDialog(required = false) {
  passwordChangeRequired = required;
  $('#currentPassword').value = '';
  $('#newPassword').value = '';
  $('#confirmPassword').value = '';
  $('#passwordHelp').textContent = required
    ? 'Replace the one-time password before configuring the tunnel.'
    : 'Enter the current password and choose a new password.';
  $('#passwordCloseBtn').hidden = required;
  $('#passwordCancelBtn').hidden = required;
  if (!$('#passwordDialog').open) $('#passwordDialog').showModal();
  $('#currentPassword').focus();
}

function render(s) {
  passwordChangeRequired = s.passwordChangeRequired;
  $('#stateLabel').textContent = s.stateLabel;
  const hero = $('.status-hero');
  hero.classList.toggle('disconnected', s.state !== 'connected');
  $('#statusIcon').textContent = s.state === 'connected' ? '✓' : '!';
  $('#siteName').textContent = s.siteName;
  $('#hostname').textContent = s.hostname;
  $('#version').textContent = s.version;
  $('#architecture').textContent = s.architecture.toUpperCase();
  $('#technicalArchitecture').textContent = s.architecture.toUpperCase();
  $('#uptime').textContent = s.uptime;
  $('#lastCheck').textContent = s.lastCheck;
  $('#cloudState').textContent = s.state === 'connected' ? 'Connected' : 'Disconnected';
  $('#connections').textContent = `${s.connections} active`;
  $('#tokenState').textContent = s.tokenPresent ? 'Configured' : 'Missing';
  $('#originState').textContent = s.originOK ? 'Available' : 'Unavailable';
  $('#tokenSummary').textContent = s.tokenPresent ? 'Configured ✓' : 'Setup required';
  $('#serviceDetail').textContent = s.serviceDetail;
  $('#securityPanel').hidden = s.externalAuth;
  $('#accessMode').textContent = s.externalAuth ? 'Secured by Ajenti' : 'Local access only';
  $('#setupBanner').hidden = !s.passwordChangeRequired && s.tokenPresent;
	$('#setupTitle').textContent = s.passwordChangeRequired ? 'Initial setup required' : 'Cloudflare token required';
	$('#setupMessage').textContent = s.passwordChangeRequired
		? 'Change the one-time administrator password, then configure the Cloudflare tunnel token.'
		: 'Configure the Cloudflare tunnel token to start remote access.';
  $('#restartBtn').disabled = s.passwordChangeRequired || !s.tokenPresent;
  $('#stopBtn').disabled = s.passwordChangeRequired || !s.tokenPresent;
  $('#tokenBtn').disabled = s.passwordChangeRequired;
  const list = $('#diagnostics');
  list.textContent = '';
  s.diagnostics.forEach(message => {
    const li = document.createElement('li');
    const time = document.createElement('time');
    const span = document.createElement('span');
    time.textContent = s.lastCheck;
    span.textContent = message;
    li.append(time, span);
    list.append(li);
  });
  if (s.passwordChangeRequired) openPasswordDialog(true);
}

async function refresh(showToast = false) {
  try {
    render(await api('/api/status'));
    if (showToast) toast('Connection check completed.');
  } catch {
    toast('Status check failed.');
  }
}

async function runAction(action, body = {}) {
  const button = action === 'restart' ? $('#restartBtn') : $('#confirmStopBtn');
  button.disabled = true;
  try {
    await api(`/api/service/${action}`, {method: 'POST', body: JSON.stringify(body)});
    toast(action === 'stop' ? 'The tunnel has been stopped.' : 'The tunnel has been restarted.');
    $('#confirmDialog').close();
    setTimeout(refresh, 1000);
  } catch {
    toast('The operation was not successful.');
  } finally {
    button.disabled = false;
  }
}

$('#checkBtn').addEventListener('click', () => refresh(true));
$('#restartBtn').addEventListener('click', () => runAction('restart'));
$('#stopBtn').addEventListener('click', () => $('#confirmDialog').showModal());
$('#confirmStopBtn').addEventListener('click', () => runAction('stop', {confirm: true}));
$('#stopCancelBtn').addEventListener('click', () => $('#confirmDialog').close());

$('#tokenBtn').addEventListener('click', () => {
  $('#tokenInput').value = '';
  $('#tokenDialog').showModal();
  $('#tokenInput').focus();
});
$('#tokenCloseBtn').addEventListener('click', () => $('#tokenDialog').close());
$('#tokenCancelBtn').addEventListener('click', () => $('#tokenDialog').close());
$('#saveTokenBtn').addEventListener('click', async () => {
  const button = $('#saveTokenBtn');
  const token = $('#tokenInput').value.trim();
  if (token.length < 80 || /\s/.test(token)) {
    toast('Enter a valid token without spaces.');
    return;
  }
  button.disabled = true;
  try {
    await api('/api/token', {method: 'POST', body: JSON.stringify({token})});
    $('#tokenInput').value = '';
    $('#tokenDialog').close();
    toast('The token was saved and the tunnel started.');
    setTimeout(refresh, 1000);
  } catch {
    toast('The token could not be saved.');
  } finally {
    button.disabled = false;
  }
});

$('#passwordBtn').addEventListener('click', () => openPasswordDialog(false));
$('#passwordCloseBtn').addEventListener('click', () => {
  if (!passwordChangeRequired) $('#passwordDialog').close();
});
$('#passwordCancelBtn').addEventListener('click', () => {
  if (!passwordChangeRequired) $('#passwordDialog').close();
});
$('#passwordDialog').addEventListener('cancel', event => {
  if (passwordChangeRequired) event.preventDefault();
});
$('#savePasswordBtn').addEventListener('click', async () => {
  const button = $('#savePasswordBtn');
  const current = $('#currentPassword').value;
  const password = $('#newPassword').value;
  const confirm = $('#confirmPassword').value;
  if (password.length < 12 || password !== confirm || password === current) {
    toast('Use 12+ characters; both new passwords must match and differ from the current password.');
    return;
  }
  button.disabled = true;
  try {
    await api('/api/password', {method: 'POST', body: JSON.stringify({current, password, confirm})});
    window.location.assign(appURL('login'));
  } catch (error) {
    toast(error.message.trim() || 'The password could not be saved.');
  } finally {
    button.disabled = false;
  }
});

setInterval(() => { $('#clock').textContent = new Date().toLocaleTimeString('en-GB'); }, 1000);
refresh();
setInterval(refresh, 15000);

