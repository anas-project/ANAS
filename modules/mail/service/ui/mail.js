'use strict';
const messages = {
  en: ['Your mailboxes','Open a mailbox to read and send mail. Aliases share its folders.','Account and addresses','Sign out','Open mailbox','Primary mailbox','Additional mailbox','Ready','Preparing configuration','Disabled · receiving continues','Retained','Unable to load mailboxes. Try again.','Opening mailbox…','Retry','This mail session has ended.','Local mail session ended. Central sign-out is unavailable.'],
  'zh-CN': ['我的邮箱','选择邮箱查看和发送邮件。同箱别名共用邮件文件夹。','账号与地址管理','退出登录','打开邮箱','主邮箱','独立邮箱','可用','配置处理中','已停用 · 继续收件','保留中','暂时无法读取邮箱，请重试。','正在打开邮箱…','重试','邮件会话已结束。','本地邮件会话已退出，统一退出尚不可用。'],
  'zh-TW': ['我的信箱','選擇信箱查看和傳送郵件。同箱別名共用郵件資料夾。','帳號與地址管理','登出','開啟信箱','主要信箱','獨立信箱','可用','正在設定','已停用 · 繼續收信','保留中','暫時無法讀取信箱，請重試。','正在開啟信箱…','重試','郵件工作階段已結束。','本機郵件已登出，統一登出尚不可用。'],
  de: ['Ihre Postfächer','Öffnen Sie ein Postfach zum Lesen und Senden. Aliase teilen dessen Ordner.','Konto und Adressen','Abmelden','Postfach öffnen','Hauptpostfach','Zusätzliches Postfach','Bereit','Konfiguration läuft','Deaktiviert · Empfang läuft weiter','Aufbewahrt','Postfächer konnten nicht geladen werden. Bitte erneut versuchen.','Postfach wird geöffnet…','Erneut versuchen','Die E-Mail-Sitzung ist beendet.','Lokal abgemeldet. Zentrale Abmeldung ist nicht verfügbar.'],
  es: ['Tus buzones','Abre un buzón para leer y enviar correo. Sus alias comparten carpetas.','Cuenta y direcciones','Cerrar sesión','Abrir buzón','Buzón principal','Buzón adicional','Disponible','Configurando','Desactivado · sigue recibiendo','Conservado','No se pudieron cargar los buzones. Inténtalo de nuevo.','Abriendo buzón…','Reintentar','La sesión de correo ha terminado.','Sesión local cerrada. El cierre central no está disponible.'],
  fr: ['Vos boîtes mail','Ouvrez une boîte pour lire et envoyer des messages. Ses alias partagent ses dossiers.','Compte et adresses','Se déconnecter','Ouvrir la boîte','Boîte principale','Boîte supplémentaire','Disponible','Configuration en cours','Désactivée · réception maintenue','Conservée','Impossible de charger les boîtes. Réessayez.','Ouverture de la boîte…','Réessayer','La session de messagerie est terminée.','Session locale fermée. La déconnexion centrale est indisponible.'],
  ja: ['自分のメールボックス','メールボックスを開いてメールを読み書きできます。別名は同じフォルダーを共有します。','アカウントとアドレス','ログアウト','メールボックスを開く','メインメールボックス','追加メールボックス','利用可能','設定中','無効 · 受信は継続','保持中','メールボックスを読み込めません。再試行してください。','メールボックスを開いています…','再試行','メールセッションが終了しました。','ローカルからログアウトしました。統合ログアウトは利用できません。'],
  ko: ['내 메일함','메일함을 열어 메일을 읽고 보낼 수 있습니다. 별칭은 같은 폴더를 공유합니다.','계정 및 주소 관리','로그아웃','메일함 열기','기본 메일함','추가 메일함','사용 가능','설정 중','사용 중지 · 수신 계속','보존 중','메일함을 불러올 수 없습니다. 다시 시도하세요.','메일함을 여는 중…','다시 시도','메일 세션이 종료되었습니다.','로컬 로그아웃 완료. 통합 로그아웃을 사용할 수 없습니다.']
};
const $ = id => document.getElementById(id);
let data, language = 'en', busy = false, generation = 0;
const text = index => messages[language][index];
const publicNotices = {
  en:'Public sending and receiving have not been verified. Contact your administrator.',
  'zh-CN':'公网收发尚未通过验证，请联系管理员。',
  'zh-TW':'公網收發尚未通過驗證，請聯絡管理員。',
  de:'Öffentlicher Empfang und Versand sind noch nicht geprüft. Bitte die Administration kontaktieren.',
  es:'El envío y la recepción públicos aún no se han verificado. Contacta con el administrador.',
  fr:'L’envoi et la réception publics ne sont pas encore vérifiés. Contactez votre administrateur.',
  ja:'外部との送受信はまだ確認されていません。管理者にお問い合わせください。',
  ko:'외부 메일 송수신이 아직 확인되지 않았습니다. 관리자에게 문의하세요.'
};
const localReady = {en:'Local mailbox ready','zh-CN':'本地邮箱可用','zh-TW':'本機信箱可用',de:'Lokales Postfach bereit',es:'Buzón local disponible',fr:'Boîte locale disponible',ja:'ローカルメールボックス利用可能',ko:'로컬 메일함 사용 가능'};
const outboundMissing = {en:'External sending is not configured.','zh-CN':'外部发信服务未配置。','zh-TW':'外部寄信服務未設定。',de:'Der externe Versand ist nicht eingerichtet.',es:'El envío externo no está configurado.',fr:'L’envoi externe n’est pas configuré.',ja:'外部送信サービスが設定されていません。',ko:'외부 발송 서비스가 설정되지 않았습니다.'};
const outboundUnknown = {en:'The sending service status is unavailable.','zh-CN':'无法读取发信服务状态。','zh-TW':'無法讀取寄信服務狀態。',de:'Der Status des Versanddienstes ist nicht verfügbar.',es:'El estado del servicio de envío no está disponible.',fr:'L’état du service d’envoi est indisponible.',ja:'送信サービスの状態を確認できません。',ko:'발송 서비스 상태를 확인할 수 없습니다.'};
function node(tag, content, className) { const element = document.createElement(tag); if (content !== undefined) element.textContent = content; if (className) element.className = className; return element; }
function render() {
  document.documentElement.lang = language;
  $('language').value = language; $('title').textContent = text(0); $('description').textContent = text(1);
  $('portal').textContent = 'ANAS'; $('logout').textContent = text(3); $('retry').textContent = text(13);
  $('setup').textContent = ({en:'Mail service setup','zh-CN':'邮件服务配置','zh-TW':'郵件服務設定',de:'Maildienst einrichten',es:'Configurar correo',fr:'Configurer la messagerie',ja:'メールサービス設定',ko:'메일 서비스 설정'})[language];
  $('setup').hidden = !data?.can_configure;
  $('mailboxes').replaceChildren(); $('person').textContent = ''; $('portal').hidden = true; $('addresses').hidden = true;
  const unverified = data && data.public_delivery_status !== 'verified';
  const sendNotice = data?.outbound_status === 'not-configured' ? outboundMissing[language] : data?.outbound_status !== 'unverified' ? outboundUnknown[language] : '';
  $('delivery-notice').hidden = !unverified; $('delivery-notice').textContent = unverified ? (sendNotice ? sendNotice + ' ' : '') + publicNotices[language] : '';
  if (!data) return;
  $('addresses').textContent = text(2); $('addresses').hidden = !data.address_management;
  $('person').textContent = data.display_name || data.username || data.mail;
  if (data.portal_url) { $('portal').href = data.portal_url + '/#/account/profile'; $('portal').hidden = false; }
  const statusKeys = {ready:7,pending:8,disabled:9,retained:10};
  for (const box of data.mailboxes) {
    const card = node('article', undefined, 'mailbox'); card.append(node('p', text(box.kind === 'primary' ? 5 : 6), 'kind'), node('h2', box.address));
    const aliases = box.addresses.filter(address => address !== box.address);
    if (aliases.length) { const list = node('ul'); for (const address of aliases) list.append(node('li', address)); card.append(list); }
    card.append(node('p', box.status === 'ready' ? localReady[language] : text(statusKeys[box.status] ?? 8), 'state'));
    const button = node('button', text(4)); button.type = 'button'; button.disabled = busy || box.status !== 'ready'; button.addEventListener('click', () => openMailbox(box.id)); card.append(button); $('mailboxes').append(card);
  }
}
async function api(path, options = {}) {
  const response = await fetch(path, {credentials:'same-origin', cache:'no-store', ...options});
  if (response.status === 401) { location.assign('/login'); throw new Error('session'); }
  if (!response.ok) throw new Error('unavailable'); return response.json();
}
async function load() {
  const current = ++generation; $('retry').hidden = true; $('status').textContent = '';
  try { const result = await api('/api/mailboxes'); if (current !== generation) return; data = result; language = messages[$('language').value] && $('language').dataset.chosen ? $('language').value : (messages[data.language] ? data.language : 'en'); render(); }
  catch { if (current === generation) { data = null; render(); $('status').textContent = text(11); $('retry').hidden = false; } }
}
async function openMailbox(id) {
  if (busy) return; busy = true; render(); $('status').textContent = text(12);
  try { const result = await api('/api/webmail/start', {method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({mailbox_id:id})});
    const target = new URL(result.post_to); if (target.protocol !== 'https:') throw new Error('target');
    const form = node('form'); form.method = 'post'; form.action = target.href;
    for (const [name,value] of Object.entries(result.fields)) { const input = node('input'); input.type = 'hidden'; input.name = name; input.value = value; form.append(input); }
    document.body.append(form); form.submit();
  } catch { busy = false; render(); $('status').textContent = text(11); }
}
$('language').addEventListener('change', () => { language = $('language').value; $('language').dataset.chosen = 'true'; render(); });
$('retry').addEventListener('click', load);
$('logout').addEventListener('click', async () => { if (busy) return; busy = true; ++generation; try { const result = await api('/api/session/logout', {method:'POST'}); data = null; render(); $('logout').disabled = true; if (result.iam_redirect) { const target = new URL(result.iam_redirect); if (target.protocol !== 'https:') throw new Error('target'); location.assign(target.href); } else { $('status').textContent = text(15); } } catch { $('status').textContent = text(11); } finally { busy = false; } });
render(); load();
