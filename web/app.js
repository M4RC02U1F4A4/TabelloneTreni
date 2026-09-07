'use strict';

/* Interfaccia dei tabelloni. Niente framework e niente passo di build: tutto
   quello che serve è una lista che si ridisegna una volta al minuto, e il
   costo di scaricare una libreria per farlo si pagherebbe a ogni apertura. */

const API = {
  stazioni: 'api/stations',
  tabellone: (da, a, arrivi) =>
    `api/board?from=${da}` + (a ? `&to=${a}` : '') + (arrivi ? '&arrivals=true' : ''),
  treno: (da, numero, a, arrivi) =>
    `api/train?from=${da}&number=${encodeURIComponent(numero)}` +
    (a ? `&to=${a}` : '') + (arrivi ? '&arrivals=true' : ''),
  linee: 'api/lines',
  avvisiLinea: (codice) => `api/lines/notices?line=${encodeURIComponent(codice)}`,
  chiavePush: 'api/push/key',
  abbonamento: 'api/push/subscribe',
};

/* Gli stati di circolazione, nell'ordine in cui li manda il server. L'indice è
   il valore numerico del campo `status`: è quello che arriva, e tradurlo in
   parola qui evita di spargere dei numeri per il resto del file. */
const STATI = [
  { classe: 'regolare', etichetta: 'regolare' },
  { classe: 'critico', etichetta: 'criticità' },
  { classe: 'grave', etichetta: 'gravi criticità' },
];
const statoLinea = (n) => STATI[n] || { classe: 'ignoto', etichetta: 'stato ignoto' };

const RINFRESCO = 60_000;   // come chiesto: una volta al minuto
const RISULTATI_MAX = 60;   // oltre, la lista diventa inutile da scorrere

const stato = {
  stazioni: [],          // [[id, nome], ...]
  canoni: [],            // stessa posizione, nome normalizzato per la ricerca
  nomi: new Map(),       // id -> nome
  da: null,
  a: null,
  arrivi: false,
  dati: null,
  scaricatoIl: 0,
  errore: null,
  caricamento: false,
  // Le linee Trenord arrivano da un servizio a parte e sono facoltative in
  // ogni punto: null vuol dire "non ancora chieste", e se la richiesta va male
  // la home si disegna lo stesso — i tabelloni sono la ragione per cui l'app
  // esiste, i bollini un di più.
  linee: null,
  lineeAggiornate: '',
  lineeErrore: null,
};

/* Le schede aperte e i viaggi già scaricati.

   Il tabellone si ridisegna intero una volta al minuto: senza tenere da parte
   quali schede erano aperte, ogni aggiornamento le richiuderebbe in faccia a
   chi le stava leggendo. I viaggi restano in mano al client per lo stesso
   motivo — un ridisegno non deve rifare le richieste già fatte. */
const aperti = new Set();
const viaggi = new Map(); // numero treno -> { stato: 'attesa'|'ok'|'errore', dati }

// Con "Modifica" attivo le righe dei preferiti mostrano la ✕. Fuori da quella
// modalità non c'è: una ✕ accanto a una riga tappabile mette la cancellazione a
// un dito dal gesto che si fa ogni giorno.
let modificaPreferiti = false;
// Quello che si sta cercando nell'elenco delle linee. Sta qui e non nel campo
// perché il campo sparisce a ogni ridisegno della pagina, e il filtro no.
let filtroLinee = '';
/* Le comunicazioni di ogni linea e quali righe sono aperte. Valgono come per le
   schede treno: un ridisegno non deve richiudere quello che si stava leggendo
   né rifare una richiesta già fatta. */
const avvisiLinea = new Map(); // codice -> { stato: 'attesa'|'ok'|'errore', dati }
const lineeAperte = new Set();
let timerRinfresco = null;
let timerEta = null;
let richiestaInCorso = 0;

const nomeStazione = (id) => stato.nomi.get(id) || `stazione ${id}`;

const $ = (sel) => document.querySelector(sel);
const testa = $('#testa');
const app = $('#app');

/* ------------------------------------------------------------------ utilità */

/* Stessa normalizzazione che fa il server sui nomi: serve a cercare "porta
   garibaldi" e trovare "MILANO PORTA GARIBALDI", punteggiatura a parte. */
function canon(s) {
  return s.normalize('NFD').replace(/[\u0300-\u036f]/g, '')
    .toUpperCase().replace(/[^A-Z0-9]+/g, ' ').trim();
}

const esc = (s) => String(s).replace(/[&<>"]/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

/* Segna nel nome il pezzo che corrisponde a quello che si sta cercando. La
   posizione si trova sul nome normalizzato e si taglia su quello vero: canon()
   non cambia la lunghezza sui casi che capitano qui, dove la punteggiatura è
   sempre un carattere per un carattere. */
function evidenzia(nome, query) {
  if (!query) return esc(nome);
  const i = canon(nome).indexOf(query);
  if (i < 0) return esc(nome);
  return esc(nome.slice(0, i)) + '<mark>' + esc(nome.slice(i, i + query.length)) +
         '</mark>' + esc(nome.slice(i + query.length));
}

/* Le frecce sono disegni, non testo.

   Un glifo come "←" non ha l'inchiostro al centro della propria riga, e di
   quanto sia spostato lo decide il font: misurato qui mezzo pixel troppo in
   basso, mentre "⇅" e "↓" stanno tre decimi troppo in alto — versi opposti, e
   su un altro sistema i valori cambiano ancora. Nessun centraggio CSS può
   rimediare, perché centra la riga di testo e non il segno che c'è dentro.

   Disegnate su una griglia di 24, invece, sono centrate per costruzione e lo
   restano su qualsiasi telefono. */
const ICONE = {
  indietro: '<path d="M19 12H5"/><path d="M12 19l-7-7 7-7"/>',
  scambia: '<path d="M8 20V4"/><path d="M4 8l4-4 4 4"/><path d="M16 4v16"/><path d="M20 16l-4 4-4-4"/>',
  giu: '<path d="M12 5v14"/><path d="M19 12l-7 7-7-7"/>',
  su: '<path d="M12 19V5"/><path d="M5 12l7-7 7 7"/>',
  // Il gallone di apertura riga: 9..15 in orizzontale, 6..18 in verticale,
  // quindi centrato — e la rotazione di 90 gradi sulla scheda aperta gira
  // attorno al suo centro vero invece che attorno al centro di una riga di
  // testo, che è il motivo per cui prima sembrava scivolare.
  gallone: '<path d="M9 6l6 6-6 6"/>',
  // Stella a cinque punte col rettangolo che la contiene centrato in 12,12:
  // il glifo "☆" del font stava tre quarti di pixel troppo in alto, e accanto
  // alla freccia ormai centrata la differenza si vedeva.
  stella: `<path d="M12 3.32L14.16 9.95L21.13 9.95L15.49 14.05L17.64 20.68L12 16.58L6.36 20.68L8.51 14.05L2.87 9.95L9.84 9.95Z"/>`,
  // La campana e il suo battaglio sono due tracciati separati: da piena, il
  // riempimento deve prendere la campana e lasciare fuori il battaglio,
  // altrimenti sotto il bordo compare una macchia che a 15px sembra sporco.
  campana: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/>' +
    '<path fill="none" d="M13.73 21a2 2 0 0 1-3.46 0"/>',
};

const icona = (nome, piena) => `<svg class="icona" viewBox="0 0 24 24" fill="${piena ? 'currentColor' : 'none'}"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
  aria-hidden="true">${ICONE[nome]}</svg>`;

function leggi(chiave, difetto) {
  try { return JSON.parse(localStorage.getItem(chiave)) ?? difetto; }
  catch { return difetto; }
}
function scrivi(chiave, valore) {
  try { localStorage.setItem(chiave, JSON.stringify(valore)); } catch { /* modalità privata */ }
}

const preferiti = () => leggi('tt.preferiti', []);
const chiaveTratta = (p) => `${p.f}>${p.t || ''}${p.a ? '>a' : ''}`;

function alternaPreferito(p) {
  const k = chiaveTratta(p);
  const elenco = preferiti().filter((x) => chiaveTratta(x) !== k);
  if (elenco.length === preferiti().length) elenco.unshift(p);
  scrivi('tt.preferiti', elenco);
}
const ePreferito = (p) => preferiti().some((x) => chiaveTratta(x) === chiaveTratta(p));

/* Le linee seguite. Si salva il codice ("S2", "R16") e non il nome, che cambia
   quando Trenord cambia un capolinea: la preferenza sopravvive al cambio. */
const campanelle = () => leggi('tt.campanelle', []);
const seguita = (codice) => campanelle().includes(codice);

function alternaCampanella(codice) {
  const elenco = campanelle().filter((c) => c !== codice);
  if (elenco.length === campanelle().length) elenco.unshift(codice);
  scrivi('tt.campanelle', elenco);
}

function ricorda(id) {
  const r = leggi('tt.recenti', []).filter((x) => x !== id);
  r.unshift(id);
  scrivi('tt.recenti', r.slice(0, 8));
}

/* ------------------------------------------------------------------- dati */

async function caricaStazioni() {
  if (stato.stazioni.length) return;
  const r = await fetch(API.stazioni);
  if (!r.ok) throw new Error('elenco stazioni non disponibile');
  const d = await r.json();
  stato.stazioni = d.stations;
  stato.canoni = d.stations.map(([, nome]) => canon(nome));
  stato.nomi = new Map(d.stations);
}

async function caricaTabellone() {
  const mio = ++richiestaInCorso;
  stato.caricamento = true;
  disegna();
  try {
    const r = await fetch(API.tabellone(stato.da, stato.a, stato.arrivi));
    if (controllaVersione(r)) return;              // la pagina si sta ricaricando
    if (mio !== richiestaInCorso) return;          // una richiesta più nuova ha già vinto
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `errore ${r.status}`);
    stato.dati = await r.json();
    stato.scaricatoIl = Date.now();
    stato.errore = null;
  } catch (e) {
    if (mio !== richiestaInCorso) return;
    stato.errore = e.message;
  } finally {
    if (mio === richiestaInCorso) {
      stato.caricamento = false;
      disegna();
    }
  }
}

/* Le linee non bloccano mai il disegno di quello che le sta intorno: la home
   compare subito e i bollini ci si appoggiano quando arrivano. Se il servizio
   non risponde si tiene da parte il motivo e si va avanti. */
async function caricaLinee() {
  try {
    const r = await fetch(API.linee);
    if (controllaVersione(r)) return;
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `errore ${r.status}`);
    const d = await r.json();
    stato.linee = d.lines || [];
    // Le linee seguite arrivano con le comunicazioni già dentro: sono quelle
    // che il servizio interroga da sé per poter mandare le notifiche, e
    // richiederle sarebbe chiedere due volte la stessa cosa.
    for (const l of stato.linee) {
      if (l.notices) avvisiLinea.set(l.code, { stato: 'ok', dati: l.notices });
    }
    stato.lineeAggiornate = d.updated || '';
    stato.lineeErrore = null;
  } catch (e) {
    stato.linee = stato.linee || [];
    stato.lineeErrore = e.message;
  }
}

/* --------------------------------------------------------------- notifiche */

/* Lo stato del permesso, che decide sia cosa si può fare sia cosa si scrive
   nella nota in cima all'elenco.

   Su iOS l'oggetto Notification esiste solo dentro l'app aggiunta alla
   schermata Home: aperta come pagina in Safari non c'è proprio, ed è il caso
   più comune di tutti. Meglio dirlo che lasciare una campanella che sembra
   funzionare e non suona mai. */
function statoNotifiche() {
  if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) {
    return 'da-installare';
  }
  // Stringa vuota vuol dire che il server ha risposto e non ha chiavi; null
  // che non gliel'abbiamo ancora chiesto, e allora non si conclude niente.
  if (chiavePubblica === '') return 'non-configurate';
  if (Notification.permission === 'denied') return 'negato';
  if (Notification.permission === 'default') return 'da-chiedere';
  return 'concesso';
}

let chiavePubblica = null;
let notificheErrore = null;

/* La chiave pubblica VAPID arriva dal server come base64url, ma `subscribe`
   vuole dei byte. Vuota significa che le notifiche non sono configurate. */
async function chiaveNotifiche() {
  // Solo una chiave vera si tiene: se il server non ne ha ancora, si richiede
  // al prossimo giro, così l'app si aggiusta da sola appena viene configurato
  // invece di restare convinta a vita che non ce ne siano.
  if (chiavePubblica) return chiavePubblica;
  const r = await fetch(API.chiavePush);
  if (!r.ok) throw new Error('notifiche non disponibili');
  chiavePubblica = (await r.json()).key || '';
  return chiavePubblica;
}

function byteDaBase64url(s) {
  const b64 = (s + '='.repeat((4 - (s.length % 4)) % 4)).replace(/-/g, '+').replace(/_/g, '/');
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
}

/* Manda al server l'elenco aggiornato delle linee seguite. Un elenco vuoto
   cancella l'abbonamento: è lo stesso gesto visto dall'altra parte.

   `permesso` è la promessa di Notification.requestPermission(), che il gestore
   del tocco ha già lanciato: su iOS quella chiamata vale solo dentro il gesto,
   quindi si fa lì e si aspetta qui. */
async function sincronizzaNotifiche(permesso) {
  if (permesso) { try { await permesso; } catch { /* prompt chiuso */ } }
  if (statoNotifiche() !== 'concesso') { aggiornaVista(); return; }

  const linee = campanelle();
  try {
    const reg = await navigator.serviceWorker.ready;
    let abbonamento = await reg.pushManager.getSubscription();
    if (!abbonamento) {
      // Senza campanelle accese non c'è niente da registrare, e non è il caso
      // di prendersi un abbonamento per poi cancellarlo subito.
      if (!linee.length) { notificheErrore = null; aggiornaVista(); return; }
      const chiave = await chiaveNotifiche();
      // Il server senza chiavi non è un guasto: è una configurazione che manca,
      // e lo dice la nota in cima all'elenco senza allarmare nessuno.
      if (!chiave) { aggiornaVista(); return; }
      abbonamento = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: byteDaBase64url(chiave),
      });
    }
    const r = await fetch(API.abbonamento, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ subscription: abbonamento, lines: linee }),
    });
    if (!r.ok) throw new Error(`il server ha risposto ${r.status}`);
    notificheErrore = null;
  } catch (e) {
    notificheErrore = e.message;
  }
  aggiornaVista();
}

/* Ridisegna il minimo indispensabile. Sull'elenco delle linee rifare la pagina
   intera cancellerebbe quello che si sta scrivendo nel campo di ricerca, e su
   un telefono con la tastiera aperta è il modo più veloce per far imprecare
   qualcuno. */
function aggiornaVista() {
  if (leggiRotta().vista !== 'linee') { disegna(); return; }
  const nota = $('#nota-notifiche');
  if (nota) {
    nota.textContent = notaNotifiche();
    nota.classList.toggle('guasta', !!notificheErrore);
  }
  disegnaElencoLinee();
}

/* Su iOS l'app installata non viene quasi mai chiusa davvero: resta sospesa in
   background per giorni, e senza questo continuerebbe a girare con il codice
   del rilascio precedente finché non la si termina a mano. Il server firma le
   risposte, e quando la firma cambia la pagina si ricarica — il service worker
   va sempre in rete per primo, quindi quello che arriva è il codice nuovo. */
let versioneVista = null;

function controllaVersione(r) {
  const v = r.headers.get('X-Versione');
  if (!v || v === versioneVista) return false;
  if (!versioneVista) { versioneVista = v; return false; }
  location.reload();
  return true;
}

/* ---------------------------------------------------------------- ricerca */

function cerca(query) {
  const q = canon(query);
  if (!q) {
    // Senza query si mostrano le stazioni usate di recente: quasi sempre la
    // scelta è una di quelle, e risparmiano di digitare.
    const recenti = leggi('tt.recenti', []).filter((id) => stato.nomi.has(id));
    return { intestazione: recenti.length ? 'Recenti' : 'Tutte le stazioni',
             voci: recenti.length ? recenti.map((id) => [id, stato.nomi.get(id)])
                                  : stato.stazioni.slice(0, RISULTATI_MAX) };
  }
  const inizio = [], parola = [], dentro = [];
  for (let i = 0; i < stato.canoni.length; i++) {
    const c = stato.canoni[i];
    const p = c.indexOf(q);
    if (p < 0) continue;
    if (p === 0) inizio.push(i);
    else if (c[p - 1] === ' ') parola.push(i);
    else dentro.push(i);
    if (inizio.length >= RISULTATI_MAX) break;
  }
  const voci = [...inizio, ...parola, ...dentro].slice(0, RISULTATI_MAX)
    .map((i) => stato.stazioni[i]);
  return { intestazione: voci.length ? null : 'Nessuna stazione', voci, query: q };
}

/* --------------------------------------------------------- selettore stazione */

const pannello = $('#scelta');
const campoCerca = $('#cerca');
const listaScelta = $('#risultati-scelta');
let campoInModifica = null;

const INVITI = {
  da: 'Stazione di partenza',
  a: 'Stazione di arrivo',
  partenze: 'Stazione di cui vedere le partenze',
  arrivi: 'Stazione di cui vedere gli arrivi',
};

function apriScelta(quale) {
  campoInModifica = quale;
  campoCerca.value = '';
  campoCerca.placeholder = INVITI[quale] || 'Cerca stazione';
  disegnaScelta();
  pannello.hidden = false;
  document.body.style.overflow = 'hidden';
  // Il focus va dato dopo che il pannello è visibile, altrimenti su iOS la
  // tastiera non compare.
  requestAnimationFrame(() => campoCerca.focus());
}

function chiudiScelta() {
  pannello.hidden = true;
  campoInModifica = null;
  document.body.style.overflow = '';
}

function disegnaScelta() {
  const { intestazione, voci, query } = cerca(campoCerca.value);
  listaScelta.innerHTML =
    (intestazione ? `<li class="intestazione">${esc(intestazione)}</li>` : '') +
    voci.map(([id, nome]) =>
      `<li><button type="button" data-id="${id}">${evidenzia(nome, query)}</button></li>`).join('');
}

listaScelta.addEventListener('click', (e) => {
  const b = e.target.closest('button[data-id]');
  if (!b) return;
  const id = Number(b.dataset.id);
  ricorda(id);
  // Un tabellone intero ha bisogno di una stazione sola, quindi non gli serve
  // il modulo con due campi: scelta la stazione, ci si va direttamente.
  if (campoInModifica === 'arrivi' || campoInModifica === 'partenze') {
    const arrivi = campoInModifica === 'arrivi';
    chiudiScelta();
    location.hash = rottaDi(id, null, arrivi);
    return;
  }
  if (campoInModifica === 'da') stato.da = id; else stato.a = id;
  chiudiScelta();
  disegna();
});
campoCerca.addEventListener('input', disegnaScelta);
$('#chiudi-scelta').addEventListener('click', chiudiScelta);
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && !pannello.hidden) chiudiScelta();
});

/* ------------------------------------------------------------------ rotte */

function leggiRotta() {
  const parti = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean);
  // #/linee/S2 apre l'elenco già filtrato su quella linea: è dove porta il
  // tocco su una notifica, che altrimenti scaricherebbe sessantacinque righe
  // addosso a chi ne stava cercando una.
  if (parti[0] === 'linee') return { vista: 'linee', filtro: decodeURIComponent(parti[1] || '') };
  if (parti[0] !== 'p' && parti[0] !== 'a') return { vista: 'home' };
  const da = Number(parti[1]);
  if (!da) return { vista: 'home' };
  return { vista: 'risultati', da, a: Number(parti[2]) || null, arrivi: parti[0] === 'a' };
}

const rottaDi = (da, a, arrivi) => `#/${arrivi ? 'a' : 'p'}/${da}` + (a && !arrivi ? `/${a}` : '');
const ROTTA_LINEE = '#/linee';

function vaiAiRisultati() {
  if (!stato.da) return;
  location.hash = rottaDi(stato.da, stato.a, stato.arrivi);
}

async function cambiaRotta() {
  const r = leggiRotta();
  fermaTimer();

  if (r.vista === 'linee') {
    // Il filtro non sopravvive all'uscita: tornandoci si vuole l'elenco
    // intero, non quello che si stava cercando mezz'ora fa. A meno che non lo
    // porti la rotta, che è il caso della notifica.
    filtroLinee = r.filtro || '';
    lineeAperte.clear();
    disegna();
    // La chiave si chiede subito, insieme alle linee: serve a sapere già prima
    // del primo tocco se il server può mandare notifiche, e quindi se ha senso
    // chiedere il permesso.
    await Promise.all([caricaLinee(), chiaveNotifiche().catch(() => {})]);
    if (leggiRotta().vista === 'linee') disegna();
    return;
  }

  if (r.vista === 'home') {
    stato.dati = null;
    stato.errore = null;
    stato.arrivi = false;
    try {
      await caricaStazioni();
    } catch (e) {
      app.innerHTML = `<p class="errore">${esc(e.message)}</p>`;
      return;
    }
    disegna();
    // I bollini arrivano da un secondo servizio e non devono far aspettare la
    // home: si ridisegna quando ci sono, e solo se nel frattempo non si è
    // andati altrove.
    Promise.all([caricaLinee(), chiaveNotifiche().catch(() => {})])
      .then(() => { if (leggiRotta().vista === 'home') disegna(); });
    return;
  }

  stato.da = r.da; stato.a = r.a; stato.arrivi = r.arrivi;
  stato.dati = null;
  stato.errore = null;
  // Schede aperte e viaggi valgono per il tabellone che si sta lasciando.
  aperti.clear();
  viaggi.clear();
  // Il catalogo pesa una ventina di KB compressi e qui non serve: i nomi delle
  // due stazioni arrivano già con il tabellone. Si scarica in sottofondo, per
  // il momento in cui si aprirà il selettore.
  caricaStazioni().catch(() => {});
  await caricaTabellone();
  avviaTimer();
}

/* ------------------------------------------------------------------ timer */

function avviaTimer() {
  fermaTimer();
  timerRinfresco = setInterval(() => {
    if (document.visibilityState === 'visible') caricaTabellone();
  }, RINFRESCO);
  // L'età del dato va aggiornata più spesso del dato stesso, altrimenti resta
  // scritto "1 minuto fa" per un minuto intero.
  timerEta = setInterval(aggiornaEta, 10_000);
}

function fermaTimer() {
  clearInterval(timerRinfresco); timerRinfresco = null;
  clearInterval(timerEta); timerEta = null;
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  const vista = leggiRotta().vista;

  // I bollini non hanno un timer che gira: senza questo, riaprendo l'app si
  // vedrebbe lo stato di quando la si è chiusa, che per un semaforo è peggio
  // che non vederlo. L'ETag rende la richiesta quasi gratis quando non è
  // cambiato niente.
  if (vista === 'home' || vista === 'linee') {
    caricaLinee().then(() => { if (leggiRotta().vista === vista) disegna(); });
    return;
  }

  if (vista !== 'risultati') return;
  // Tornando sull'app dopo un po', il tabellone è vecchio: si aggiorna subito
  // invece di aspettare il prossimo giro.
  if (Date.now() - stato.scaricatoIl > RINFRESCO / 2) caricaTabellone();
  else aggiornaEta();
});

function eta() {
  if (!stato.scaricatoIl) return '';
  const s = Math.round((Date.now() - stato.scaricatoIl) / 1000);
  if (s < 10) return 'adesso';
  if (s < 60) return `${s} secondi fa`;
  const m = Math.round(s / 60);
  return m === 1 ? 'un minuto fa' : `${m} minuti fa`;
}

function aggiornaEta() {
  const el = $('#eta');
  if (el) el.textContent = eta();
}

/* ------------------------------------------------------------------ vista */

function disegna() {
  const r = leggiRotta();
  if (r.vista === 'home') disegnaHome();
  else if (r.vista === 'linee') disegnaLinee();
  else disegnaRisultati();
}

function disegnaHome() {
  testa.innerHTML = `
    <div class="testa-riga"><h1 class="titolo">Tabellone Treni</h1></div>
    <div class="sottotitolo">Partenze e arrivi RFI, filtrati per dove devi andare</div>`;

  const fav = preferiti();
  if (!fav.length) modificaPreferiti = false;

  app.innerHTML = `
    ${fav.length ? `
    <section class="sezione">
      <div class="testa-sezione">
        <h2 class="etichetta-sezione">Preferiti</h2>
        <button class="btn-testo piccolo" type="button" data-modifica>
          ${modificaPreferiti ? 'Fine' : 'Modifica'}</button>
      </div>
      <ul class="lista">${fav.map((p) => rigaPreferito(p)).join('')}</ul>
    </section>` : ''}

    <section class="sezione">
      <h2 class="etichetta-sezione">Nuova ricerca</h2>
      <div class="gruppo">
        <div class="gruppo-campi">
          ${campoStazione('da', 'DA', stato.da, 'Stazione di partenza')}
          ${campoStazione('a', 'A', stato.a, 'Tutte le destinazioni')}
        </div>
        <button class="inverti" type="button" data-scambia
                aria-label="Inverti partenza e arrivo">${icona('scambia')}</button>
      </div>
      <button class="principale" type="button" data-vai ${stato.da ? '' : 'disabled'}>
        Vedi i treni
      </button>
    </section>

    <section class="sezione">
      <h2 class="etichetta-sezione">Tabellone di una stazione</h2>
      <ul class="lista">
        <li class="riga">
          <button class="riga-tocco" type="button" data-apri="partenze">
            <span class="segno tenue">${icona('su')}</span>
            <span class="testo">Partenze di una stazione</span>
            <span class="chevron">${icona('gallone')}</span>
          </button>
        </li>
        <li class="riga">
          <button class="riga-tocco" type="button" data-apri="arrivi">
            <span class="segno tenue">${icona('giu')}</span>
            <span class="testo">Arrivi di una stazione</span>
            <span class="chevron">${icona('gallone')}</span>
          </button>
        </li>
      </ul>
    </section>

    ${sezioneLinee()}`;
}

/* La sezione in home non elenca tutte e 65 le linee: mostra quelle seguite e
   quelle che in questo momento hanno un problema. In una giornata normale sono
   zero righe, ed è l'informazione giusta — la lista intera sta a un tocco. */
function sezioneLinee() {
  const testa = `
    <div class="testa-sezione">
      <h2 class="etichetta-sezione">Stato linee</h2>
      <a class="btn-testo piccolo" href="${ROTTA_LINEE}">Tutte</a>
    </div>`;

  if (stato.linee === null) {
    return `<section class="sezione">${testa}<ul class="lista">${rigaLineaScheletro()}</ul></section>`;
  }
  if (stato.lineeErrore) {
    // Sottovoce: che manchino i bollini non deve sembrare che sia rotto il
    // tabellone, che è l'unica cosa per cui l'app si apre di corsa.
    return `<section class="sezione">${testa}
      <p class="nota">Stato delle linee non disponibile.</p></section>`;
  }

  const mie = stato.linee.filter((l) => seguita(l.code));
  const guai = stato.linee.filter((l) => l.status > 0 && !seguita(l.code));
  const righe = [...mie, ...guai];

  if (!righe.length) {
    return `<section class="sezione">${testa}
      <ul class="lista"><li class="riga">
        <span class="riga-tocco statica">
          <span class="segno"><span class="bollino regolare"></span></span>
          <span class="testo">Tutte le linee sono regolari</span>
        </span>
      </li></ul></section>`;
  }
  return `<section class="sezione">${testa}
    <ul class="lista">${righe.map(rigaLinea).join('')}</ul></section>`;
}

function rigaLinea(l, query) {
  const st = statoLinea(l.status);
  const accesa = seguita(l.code);
  const campanella = `<button class="campanella${accesa ? ' accesa' : ''}" type="button"
      data-campanella="${esc(l.code)}" aria-pressed="${accesa}"
      aria-label="${accesa ? 'Smetti di seguire' : 'Segui'} ${esc(l.name)}"
      >${icona('campana', accesa)}</button>`;
  const nome = `<span class="segno"><span class="bollino ${st.classe}"></span></span>
    <span class="testo">${evidenzia(l.name, query)}<span class="qualifica"> · ${st.etichetta}</span></span>`;

  // Il conteggio si mostra solo quando si sa: prima di aprire, di una linea che
  // nessuno segue non sappiamo ancora se ha comunicazioni.
  const noto = avvisiLinea.get(l.code);
  const quante = noto && noto.stato === 'ok' ? noto.dati.length : null;

  return `<li class="riga con-avvisi">
    <details class="avvisi-linea" data-linea="${esc(l.code)}"${lineeAperte.has(l.code) ? ' open' : ''}>
      <summary class="riga-tocco">
        ${nome}
        ${quante !== null ? `<span class="conta-avvisi">${quante}</span>` : ''}
        <span class="chevron">${icona('gallone')}</span>
      </summary>
      ${corpoAvvisi(l.code)}
    </details>
    ${campanella}
  </li>`;
}

function corpoAvvisi(codice) {
  const v = avvisiLinea.get(codice);
  if (!v || v.stato === 'attesa') {
    return '<p class="avvisi-attesa">Cerco le comunicazioni…</p>';
  }
  if (v.stato === 'errore') {
    return `<p class="avvisi-attesa">${esc(v.dati)}</p>`;
  }
  if (!v.dati.length) {
    return '<p class="avvisi-attesa">Nessuna comunicazione su questa linea.</p>';
  }
  return `<ol class="avvisi">${v.dati.map(vociAvviso).join('')}</ol>`;
}

/* Le comunicazioni si chiedono quando si apre una riga, non per tutte e 65: il
   dettaglio di una linea pesa più di cento KB, e di righe se ne apre una. */
async function scaricaAvvisi(codice) {
  if (avvisiLinea.has(codice)) return;
  avvisiLinea.set(codice, { stato: 'attesa' });
  try {
    const r = await fetch(API.avvisiLinea(codice));
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `errore ${r.status}`);
    avvisiLinea.set(codice, { stato: 'ok', dati: (await r.json()).notices || [] });
  } catch (e) {
    avvisiLinea.set(codice, { stato: 'errore', dati: e.message });
  }
  if (leggiRotta().vista === 'linee') disegnaElencoLinee();
}

/* Il testo arriva da Trenord e va messo con esc(): sono comunicazioni scritte a
   mano in sala operativa, e ci finiscono dentro indirizzi e virgolette. */
function vociAvviso(a) {
  const d = a.date ? new Date(a.date) : null;
  const quando = d && !isNaN(d)
    ? d.toLocaleDateString('it-IT', { day: 'numeric', month: 'long' }) : '';
  return `<li>
    ${quando ? `<span class="data-avviso">${esc(quando)}</span>` : ''}
    <span class="testo-avviso">${esc(a.text)}</span>
  </li>`;
}

/* Lo scheletro tiene anche il posto della campanella: senza, all'arrivo dei
   dati la colonna di destra compariva di colpo e la lista sussultava. */
function rigaLineaScheletro() {
  return `<li class="riga scheletro">
    <span class="riga-tocco statica">
      <span class="segno"><span class="bollino"></span></span>
      <span class="testo"><span class="barra b-dest"></span></span>
    </span>
    <span class="campanella" aria-hidden="true">${icona('campana')}</span>
  </li>`;
}

function disegnaLinee() {
  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">Stato linee</h1>
    </div>
    <div class="sottotitolo">Circolazione Trenord${
      stato.lineeAggiornate ? ` · <span class="${lineeFerme() ? 'fermo' : 'vivo'}">letto ${
        esc(oraDi(stato.lineeAggiornate))}</span>` : ''}</div>`;

  if (stato.linee === null) {
    app.innerHTML = `<ul class="lista">${rigaLineaScheletro().repeat(8)}</ul>`;
    return;
  }
  if (stato.lineeErrore) {
    app.innerHTML = `<p class="errore">${esc(stato.lineeErrore)}</p>`;
    return;
  }

  // Il campo di ricerca sta fuori dal contenitore che si ridisegna: filtrando
  // si riscrive solo l'elenco, e quello che si sta scrivendo — con il cursore
  // dov'era — resta al suo posto.
  app.innerHTML = `
    <input id="cerca-linee" class="cerca cerca-linee" type="search" inputmode="search"
           autocomplete="off" autocorrect="off" spellcheck="false"
           placeholder="Filtra per linea o stazione" aria-label="Filtra le linee"
           value="${esc(filtroLinee)}">
    <p id="nota-notifiche" class="nota${notificheErrore ? ' guasta' : ''}">${esc(notaNotifiche())}</p>
    <div id="elenco-linee"></div>`;
  disegnaElencoLinee();
}

/* Il filtro guarda il nome e il codice. Il nome di una linea è la catena delle
   sue stazioni — "Saronno-Milano Passante-Lodi" — quindi cercare una stazione
   funziona senza doverle indicizzare a parte. */
function lineeFiltrate() {
  const q = canon(filtroLinee);
  if (!q) return { linee: stato.linee, query: '' };
  // Il codice si confronta senza spazi: canon() trasforma "RE_13" in "RE 13",
  // ma chi lo cerca lo scrive attaccato.
  const qs = q.replace(/ /g, '');
  const linee = stato.linee.filter((l) =>
    canon(l.name).includes(q) || canon(l.code).replace(/ /g, '').includes(qs));
  return { linee, query: q };
}

function disegnaElencoLinee() {
  const dove = $('#elenco-linee');
  if (!dove) return;
  const { linee, query } = lineeFiltrate();

  if (!linee.length) {
    dove.innerHTML = `<p class="nota">Nessuna linea per «${esc(filtroLinee)}».</p>`;
    return;
  }

  // I gruppi si prendono nell'ordine in cui arrivano invece di ordinarli:
  // è quello in cui Trenord li pubblica, e chi conosce le proprie linee le
  // cerca dove è abituato a trovarle. Un gruppo rimasto senza linee sparisce,
  // altrimenti filtrando resterebbero delle intestazioni sopra il vuoto.
  const gruppi = [];
  for (const l of linee) {
    const ultimo = gruppi[gruppi.length - 1];
    if (ultimo && ultimo.nome === l.group) ultimo.linee.push(l);
    else gruppi.push({ nome: l.group, linee: [l] });
  }

  dove.innerHTML = gruppi.map((g) => `
    <section class="sezione">
      <h2 class="etichetta-sezione">${esc(g.nome)}</h2>
      <ul class="lista">${g.linee.map((l) => rigaLinea(l, query)).join('')}</ul>
    </section>`).join('');
}

/* Cosa succede quando accendi una campanella, detto prima di accenderla. Una
   campanella che non suona è una promessa mancata, e il posto per dirlo è
   questo, non la schermata di blocco alle sette di sera. */
function notaNotifiche() {
  if (notificheErrore) return `Notifiche non attivate: ${notificheErrore}.`;
  const quante = campanelle().length;
  switch (statoNotifiche()) {
    case 'da-installare':
      return 'Le notifiche funzionano solo con l\'app aggiunta alla schermata Home del telefono. La campanella intanto tiene la linea in cima alla home.';
    case 'non-configurate':
      return 'Le notifiche non sono ancora configurate sul server. La campanella intanto tiene la linea in cima alla home.';
    case 'negato':
      return 'Le notifiche sono bloccate per questo sito: si riattivano dalle impostazioni del telefono. La campanella tiene comunque la linea in cima alla home.';
    case 'da-chiedere':
      return 'La prima campanella accesa chiede il permesso di mandarti le notifiche.';
    default:
      return quante
        ? `Ti avvisiamo quando cambia il bollino ${quante === 1 ? 'della linea seguita' : 'di una delle linee seguite'}.`
        : 'Accendi una campanella per essere avvisato quando cambia il bollino di una linea.';
  }
}

/* Il servizio rilegge Trenord ogni cinque minuti: passati i dodici, di letture
   ne sono saltate almeno due e quello che si sta guardando non è più lo stato
   della circolazione ma il ricordo di com'era. Un semaforo fermo che non lo
   dice è peggio di un semaforo spento. */
function lineeFerme() {
  const t = Date.parse(stato.lineeAggiornate);
  return !isNaN(t) && Date.now() - t > 12 * 60_000;
}

/* L'orario di lettura arriva in UTC dal servizio; qui si mostra nell'ora del
   telefono, che per un dato aggiornato pochi minuti fa è quello che serve. */
function oraDi(iso) {
  const d = new Date(iso);
  return isNaN(d) ? '' : d.toLocaleTimeString('it-IT', { hour: '2-digit', minute: '2-digit' });
}

function campoStazione(quale, sigla, id, vuoto) {
  return `<button class="campo" type="button" data-apri="${quale}">
    <span class="sigla">${sigla}</span>
    <span class="valore ${id ? '' : 'vuoto'}">${id ? esc(nomeStazione(id)) : vuoto}</span>
    <span class="chevron">${icona('gallone')}</span>
  </button>`;
}

function rigaPreferito(p) {
  return `<li class="riga">
    <a class="riga-tocco" href="${rottaDi(p.f, p.t, p.a)}">
      <span class="segno">${icona('stella', true)}</span>
      <span class="testo">${etichettaPreferito(p)}</span>
      ${modificaPreferiti ? '' : `<span class="chevron">${icona('gallone')}</span>`}
    </a>
    ${modificaPreferiti ? `<button class="togli" type="button" data-togli="${esc(chiaveTratta(p))}"
        aria-label="Togli dai preferiti">✕</button>` : ''}
  </li>`;
}

function etichettaPreferito(p) {
  if (p.a) return `${esc(nomeStazione(p.f))}<span class="qualifica"> · arrivi</span>`;
  if (p.t) return `${esc(nomeStazione(p.f))} <span class="freccia">→</span> ${esc(nomeStazione(p.t))}`;
  return `${esc(nomeStazione(p.f))}<span class="qualifica"> · tutte le partenze</span>`;
}

function disegnaRisultati() {
  const d = stato.dati;
  const daNome = (d && d.from) || (stato.da ? nomeStazione(stato.da) : '');
  const aNome = (d && d.to) || (stato.a ? nomeStazione(stato.a) : '');
  const questa = { f: stato.da, t: stato.arrivi ? null : stato.a, a: stato.arrivi || undefined };
  const salvato = ePreferito(questa);

  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">${esc(daNome)}${aNome && !stato.arrivi ?
        ` <span class="freccia">→</span> ${esc(aNome)}` : ''}</h1>
      <button class="tasto" type="button" data-preferito aria-pressed="${salvato}"
              aria-label="${salvato ? 'Togli dai preferiti' : 'Aggiungi ai preferiti'}">${icona('stella', salvato)}</button>
    </div>
    <div class="sottotitolo">
      ${stato.arrivi ? 'Arrivi' : 'Partenze'} ·
      <span class="${stato.caricamento ? 'fermo' : 'vivo'}">
        ${stato.caricamento ? 'aggiornamento…' : `aggiornato <span id="eta">${eta()}</span>`}
      </span>
    </div>`;

  if (!d) {
    app.innerHTML = stato.errore
      ? `<p class="errore">${esc(stato.errore)}</p>`
      : `<ul>${scheletro().repeat(5)}</ul>`;
    return;
  }

  const note = [];
  if (stato.errore) note.push(`<p class="errore">${esc(stato.errore)} — mostrati gli ultimi dati ricevuti.</p>`);
  if (d.stopsUnavailable) {
    note.push(`<p class="nota">RFI non pubblica le fermate dei treni in arrivo:
      qui sotto ci sono tutti gli arrivi, senza il filtro per ${esc(aNome)}.</p>`);
  }
  // A zero treni il conteggio ripeterebbe quello che dice già lo stato vuoto.
  if (d.filtered && d.trains.length > 0) {
    const n = d.trains.length;
    note.push(`<p class="nota">${n} ${n === 1 ? 'treno' : 'treni'} su ${d.total}
      ${n === 1 ? 'ferma' : 'fermano'} a ${esc(aNome)}.</p>`);
  }

  const corpo = d.trains.length
    ? `<ul>${d.trains.map((t) => rigaTreno(t, d, conMisure(d))).join('')}</ul>`
    : `<div class="senza-risultati">
         <p>Nessun treno da ${esc(daNome)}${d.filtered ? ` che ferma a ${esc(aNome)}` : ''}.</p>
         <p>Il tabellone copre solo le prossime ore.</p>
       </div>`;

  app.innerHTML = note.join('') + legenda(d) + corpo;
}

// Ha la forma di una scheda vera, così l'elenco non sobbalza quando i dati
// arrivano, ma con le barre al posto del testo.
const scheletro = () => `<li class="treno scheletro" aria-hidden="true">
  <div class="riga-treno">
    <div class="orario"><span class="barra b-ora"></span></div>
    <div class="dove"><span class="barra b-dest"></span><span class="barra b-meta"></span></div>
  </div>
</li>`;

/* I due ritardi.

   RFI pubblica il proprio con parsimonia: sotto i pochi minuti arrotonda a
   zero — cinque treni su un campione di venti viaggiavano fra +1 e +2 con il
   tabellone che dichiarava zero — e sopra l'ora si aggiorna con calma, tanto
   che un treno da 95 minuti ne dichiarava 70.

   Quale delle due sia quella giusta non lo decide l'app: si mostrano tutt'e
   due, e il colore dice da dove viene il numero. Che il treno sia in ritardo lo
   dice invece l'orario, che diventa rosso: era il colore che aveva prima il
   ritardo, e resta il segnale da leggere di sfuggita.

   La pastiglia di ViaggiaTreno manca finché il treno non è stato rilevato da
   qualche parte: lì una misura non esiste, e uno zero al suo posto sarebbe una
   puntualità che nessuno ha visto. */
const ritardoLive = (t) => (typeof t.liveDelay === 'number' ? t.liveDelay : null);
const ritardoRFI = (t) => (t.cancelled ? null : (typeof t.delay === 'number' ? t.delay : 0));
/* Il ritardo che conta per il colore dell'ora: la misura sul treno quando c'è,
   altrimenti quel che dice il tabellone. */
const ritardoVero = (t) => (ritardoLive(t) ?? ritardoRFI(t));

const segnoRitardo = (m) => (m > 0 ? `+${m}` : String(m));

function scarti(t, riservaVT) {
  if (t.cancelled) return '<span class="scarto solo">soppresso</span>';
  // RFI ogni tanto scrive nella cella del ritardo un testo invece di un numero
  // ("RITARDO", per un ritardo annunciato ma non ancora quantificato): quello
  // va riportato tale e quale, non tradotto in una cifra che non ha mandato.
  if (t.status) return `<span class="scarto solo">${esc(t.status.toLowerCase())}</span>`;

  const live = ritardoLive(t);
  // Posto vuoto al posto della misura mancante: senza, la pastiglia di RFI
  // scivolerebbe a destra, proprio dove sulle altre righe c'è quella di
  // ViaggiaTreno. Il posto si riserva solo se in lista una misura c'è: quando
  // non ne ha nessuno sarebbe una colonna vuota per tutto il tabellone.
  const seconda = live !== null
    ? `<span class="scarto vt">${segnoRitardo(live)}</span>`
    : (riservaVT ? '<span class="scarto vt vuota"></span>' : '');
  return `<span class="scarti">
    <span class="scarto rfi">${segnoRitardo(ritardoRFI(t))}</span>
    ${seconda}
  </span>`;
}

/** Se in lista almeno un treno è stato rilevato, la seconda colonna esiste. */
const conMisure = (d) => d.trains.some((t) => ritardoLive(t) !== null);

/* La legenda compare solo se almeno un treno porta la misura di ViaggiaTreno:
   con la sola colonna di RFI non ci sarebbero due colori da spiegare. */
function legenda(d) {
  if (!conMisure(d)) return '';
  return `<p class="legenda">
    <span class="scarto rfi campione"></span> tabellone RFI
    <span class="scarto vt campione"></span> misurato sul treno
  </p>`;
}

function rigaTreno(t, d, misure) {
  const soppresso = t.cancelled;
  const classi = ['treno'];
  if (soppresso) classi.push('soppresso');
  else if (t.boarding) classi.push('parte');
  if (!soppresso && ritardoVero(t) > 0) classi.push('in-ritardo');

  const scarto = scarti(t, misure);

  // Su alcuni treni RFI ripete la categoria anche come vettore
  // ("INTERCITY NOTTE · INTERCITY NOTTE"): si scrive una volta sola.
  const vettore = t.carrier && canon(t.carrier) !== canon(t.category || '') ? t.carrier : '';
  // Con l'orario di arrivo la riga non ci sta tutta e verrebbe troncata: cede
  // il posto il vettore, che è il campo che informa meno — RFI stesso lo mostra
  // come logo, e su una tratta regionale è quasi sempre lo stesso.
  const dettagli = [
    t.arrival ? `<span class="arrivo">arrivo ${esc(t.arrival)}</span>` : '',
    esc([t.category, t.number].filter(Boolean).join(' ')),
    t.arrival ? '' : esc(vettore),
  ].filter(Boolean).join(' · ');

  // Il binario cambiato: si dice da quale, non solo che è successo.
  //
  // Quale dei due numeri sia la novità dipende da chi è avanti fra le due
  // fonti. Se il tabellone mostra già quello nuovo, la cosa da aggiungere è
  // quello vecchio, per chi si è incamminato prima; se invece è rimasto
  // indietro sul previsto, la cosa da aggiungere è quello nuovo. Fuori da
  // questi due casi le due fonti dicono tre numeri diversi, e allora l'unica
  // cosa onesta è dire che è cambiato senza pretendere di sapere in quale
  // direzione.
  let cambio = null;
  if (t.platformChanged) {
    if (t.platform === t.platformActual && t.platformScheduled) cambio = `era ${t.platformScheduled}`;
    else if (t.platform === t.platformScheduled && t.platformActual) cambio = `ora ${t.platformActual}`;
    else cambio = 'cambiato';
  }

  const espandibile = t.stops && t.stops.length > 0;
  const contenuto = `
    <div class="orario">
      <span class="ora ${soppresso ? 'barrato' : ''}">${esc(t.time)}</span>
      ${scarto}
    </div>
    <div class="dove">
      <div class="destinazione">${d.arrivals ? '<span class="da">da</span> ' : ''}${esc(t.terminus)}</div>
      <span class="meta">${dettagli}</span>
    </div>
    <div class="binario${cambio ? ' cambiato' : ''}">
      ${t.platform ? `<span class="num">${esc(t.platform)}</span>
                      <span class="cap">${cambio ? esc(cambio) : 'BIN'}</span>`
                   : '<span class="ignoto" title="Binario non ancora assegnato">–</span>'}
    </div>
    ${espandibile ? `<span class="apri" aria-hidden="true">${icona('gallone')}</span>` : ''}
    ${t.notes ? `<div class="avviso">${esc(t.notes)}</div>` : ''}`;

  const riga = `riga-treno${espandibile ? ' espandibile' : ''}`;
  if (!espandibile) {
    return `<li class="${classi.join(' ')}"><div class="${riga}">${contenuto}</div></li>`;
  }
  // La scheda intera è il <summary>: toccare il treno apre le sue fermate.
  return `<li class="${classi.join(' ')}">
    <details data-treno="${esc(t.number)}"${aperti.has(t.number) ? ' open' : ''}>
      <summary class="${riga}">${contenuto}</summary>
      ${fermate(t)}
    </details>
  </li>`;
}

function fermate(t) {
  const viaggio = viaggi.get(t.number);
  if (viaggio && viaggio.stato === 'ok' && viaggio.dati.stops?.length) return viaggioReale(t, viaggio.dati);

  // La fermata che interessa è quella su cui il filtro ha agganciato il treno:
  // la si riconosce dall'orario di arrivo, e va evidenziata una volta sola —
  // un treno può ripassare a orari diversi ma non due volte allo stesso.
  const evidenziata = t.arrival ? t.stops.findIndex((f) => f.time === t.arrival) : -1;
  const voci = t.stops.map((f, i) =>
    `<li class="${i === evidenziata ? 'meta-scelta' : ''}">
      <span>${esc(f.name)}</span><time>${esc(f.time)}</time></li>`).join('');
  // Le fermate previste restano leggibili in ogni caso: sostituirle con
  // un'attesa toglierebbe un'informazione che c'è già. Sotto, però, va detto
  // com'è andata la ricerca del viaggio vero — anche quando è andata a vuoto,
  // altrimenti chi ha toccato la scheda resta senza risposta.
  const note = {
    attesa: 'cerco dov\'è il treno…',
    ok: 'ViaggiaTreno non segue questo treno',
    errore: 'viaggio non disponibile adesso',
  };
  const nota = viaggio ? `<p class="viaggio-nota">${note[viaggio.stato]}</p>` : '';
  return `<ol class="fermate">${voci}</ol>${nota}`;
}

/* Le fermate secondo ViaggiaTreno: quelle già servite portano l'ora a cui il
   treno ci è passato davvero, le altre solo quella prevista.

   Non si prova a proiettare il ritardo sulle fermate future: ViaggiaTreno non
   lo fa — lì lascia zero, che è un campo non compilato e non una previsione — e
   inventarlo qui vorrebbe dire stampare un orario che nessuno ha calcolato,
   con l'aria di essere un dato. */
function viaggioReale(t, d) {
  const voci = d.stops.map((f) => {
    const classi = [];
    if (f.passed) classi.push('passata');
    if (f.chosen) classi.push('meta-scelta');
    const ora = f.passed && f.actual
      ? `${esc(f.actual)}${f.delay ? ` <small>${f.delay > 0 ? '+' : ''}${f.delay}</small>` : ''}`
      : esc(f.scheduled);
    return `<li class="${classi.join(' ')}"><span>${esc(f.name)}</span><time>${ora}</time></li>`;
  }).join('');

  const dove = d.tracked && d.lastSeen?.station
    ? `<p class="viaggio-nota">rilevato a ${esc(d.lastSeen.station)}${
        d.lastSeen.time ? ` alle ${esc(d.lastSeen.time)}` : ''}</p>`
    : '<p class="viaggio-nota">non ancora partito</p>';
  return `${dove}<ol class="fermate">${voci}</ol>`;
}

/* Scarica il viaggio di un treno, una volta sola per treno.

   Parte solo quando qualcuno apre la scheda: è una richiesta per treno su un
   servizio lento, e farla per tutti e quaranta i treni di un tabellone
   significherebbe pagarla quaranta volte per le due o tre schede che si aprono
   davvero. */
async function scaricaViaggio(numero) {
  if (viaggi.has(numero)) return;
  viaggi.set(numero, { stato: 'attesa' });
  disegna();
  try {
    const r = await fetch(API.treno(stato.da, numero, stato.a, stato.arrivi));
    if (!r.ok) throw new Error(`errore ${r.status}`);
    viaggi.set(numero, { stato: 'ok', dati: await r.json() });
  } catch {
    // Un viaggio che non arriva non è un guasto della pagina: restano le
    // fermate previste, che è quello che si vedeva prima di questa aggiunta.
    viaggi.set(numero, { stato: 'errore' });
  }
  disegna();
}

/* ------------------------------------------------------------------ eventi */

app.addEventListener('click', (e) => {
  const t = e.target;
  if (t.closest('[data-apri]')) apriScelta(t.closest('[data-apri]').dataset.apri);
  else if (t.closest('[data-vai]')) vaiAiRisultati();
  else if (t.closest('[data-scambia]')) { [stato.da, stato.a] = [stato.a, stato.da]; disegna(); }
  else if (t.closest('[data-modifica]')) { modificaPreferiti = !modificaPreferiti; disegna(); }
  else if (t.closest('[data-campanella]')) {
    const codice = t.closest('[data-campanella]').dataset.campanella;
    const accendo = !seguita(codice);
    alternaCampanella(codice);
    // Il permesso si chiede qui e non dentro sincronizzaNotifiche: su iOS
    // vale solo se la chiamata parte durante il tocco, e dopo un await il
    // tocco non c'è più. La promessa la si aspetta di là.
    const permesso = accendo && statoNotifiche() === 'da-chiedere'
      ? Notification.requestPermission() : null;
    aggiornaVista();
    sincronizzaNotifiche(permesso);
  }
  else if (t.closest('[data-togli]')) {
    const k = t.closest('[data-togli]').dataset.togli;
    scrivi('tt.preferiti', preferiti().filter((p) => chiaveTratta(p) !== k));
    disegna();
  }
});

app.addEventListener('input', (e) => {
  if (e.target.id !== 'cerca-linee') return;
  filtroLinee = e.target.value;
  disegnaElencoLinee();
});

// `toggle` non fa bubbling: si ascolta in fase di cattura sul contenitore.
app.addEventListener('toggle', (e) => {
  const d = e.target;
  if (d instanceof HTMLDetailsElement && d.dataset.linea) {
    const codice = d.dataset.linea;
    if (d.open) { lineeAperte.add(codice); scaricaAvvisi(codice); }
    else lineeAperte.delete(codice);
    return;
  }
  if (!(d instanceof HTMLDetailsElement) || !d.dataset.treno) return;
  const numero = d.dataset.treno;
  if (d.open) {
    aperti.add(numero);
    scaricaViaggio(numero);
  } else {
    aperti.delete(numero);
  }
}, true);

testa.addEventListener('click', (e) => {
  if (!e.target.closest('[data-preferito]')) return;
  alternaPreferito({ f: stato.da, t: stato.arrivi ? null : stato.a, a: stato.arrivi || undefined });
  disegnaRisultati();
});

window.addEventListener('hashchange', cambiaRotta);
cambiaRotta();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('sw.js').catch(() => {});
    // Riallinea l'abbonamento a ogni avvio: il servizio potrebbe averlo perso,
    // e chi ha una campanella accesa non deve accorgersene.
    if (campanelle().length) sincronizzaNotifiche();
  });
}
