/* Le schermate del README, dentro un telefono, in tema chiaro e scuro.
 *
 * Tutto quello che si vede è inventato: tabelloni, viaggi, avvisi e linee
 * escono da qui, non da RFI o da Trenord. Una schermata vera mostra quello che
 * passava in quel minuto in quella stazione, e il README è pubblico. Dal server
 * vero arriva solo il catalogo delle stazioni, che è già nel repo.
 *
 * Anche l'orologio è fermo, a un mercoledì alle 17:52: "fra 10 min", il
 * treno di oggi e l'ora nella barra di stato restano gli stessi a ogni giro.
 *
 * Gira in locale contro un server avviato su :18080, come ci/schermate.js:
 *   go build -o /tmp/tt . && PORT=18080 STATO_LINEE_URL=http://localhost:1 /tmp/tt &
 *   PW=~/.npm/_npx/<hash>/node_modules/playwright node ci/vetrina.js
 * e scrive in docs/.
 */
const path = require('node:path');
const fs = require('node:fs');

const ADESSO = new Date('2026-10-14T17:52:00+02:00');
const MEZZANOTTE = new Date('2026-10-14T00:00:00+02:00').getTime();
const ORA = '17:52';

// L'iPhone 18 Pro, in punti: lo schermo da 6,3" del 17 Pro.
const SCHERMO = { width: 402, height: 874 };
const BARRA = 54; // la barra di stato, sopra la pagina installata
const FONDO = 34; // la fascia dell'indicatore, che la pagina lascia libera
const SCALA = 2;

const TERMINI = 2416;
const TIBURTINA = 2385;
const OSTIENSE = 2379;
const FIUMICINO = 1327;
const NAPOLI = 1888;

/* ------------------------------------------------------------- il mondo */

const fermata = (name, time) => ({ name, time });

// Le partenze da Roma Termini alle 17:52. I treni hanno le fermate successive
// come le pubblica RFI, ed è da quelle che il server ricava le tratte: qui le
// tratte si ricavano allo stesso modo, filtrando questo stesso elenco.
const PARTENZE_TERMINI = [
  { number: '9541', category: "ALTA VELOCITA'", carrier: 'FRECCIAROSSA', terminus: 'NAPOLI CENTRALE', time: '17:55',
    delay: 5, live: 7, platform: '12', boarding: true, notes: 'CARROZZA 1 IN TESTA AL TRENO',
    stops: [fermata('NAPOLI AFRAGOLA', '18:58'), fermata('NAPOLI CENTRALE', '19:08')] },
  { number: '8931', category: "ALTA VELOCITA'", carrier: 'ITALO', terminus: 'SALERNO', time: '17:58',
    delay: 18, live: 21, platform: '', notes: 'CARROZZA 1 IN CODA AL TRENO',
    stops: [fermata('NAPOLI CENTRALE', '19:10'), fermata('SALERNO', '19:50')] },
  { number: '4520', category: 'REG', carrier: 'TRENITALIA', terminus: 'FOLIGNO', time: '18:00',
    platform: '1EST', boarding: true, notes: 'BINARIO DECENTRATO', live: 0,
    stops: [fermata('ROMA TIBURTINA', '18:07'), fermata('ORTE', '18:45'), fermata('TERNI', '19:05'),
      fermata('SPOLETO', '19:31'), fermata('FOLIGNO', '19:56')] },
  { number: '12536', category: 'REG', carrier: 'TRENITALIA', terminus: 'CIVITAVECCHIA', time: '18:03',
    platform: '27', live: 2,
    stops: [fermata('ROMA OSTIENSE', '18:09'), fermata('ROMA SAN PIETRO', '18:14'), fermata('LADISPOLI-CERVETERI', '18:42'),
      fermata('CIVITAVECCHIA', '19:08')] },
  { number: '594', category: 'IC', carrier: 'TRENITALIA', terminus: 'MILANO CENTRALE', time: '18:05',
    delay: 3, live: 4, platform: '9', platformScheduled: '6',
    stops: [fermata('ROMA TIBURTINA', '18:13'), fermata('ORVIETO', '19:11'), fermata('FIRENZE SANTA MARIA NOVELLA', '20:40'),
      fermata('BOLOGNA CENTRALE', '21:52'), fermata('MILANO CENTRALE', '00:15')] },
  { number: '4522', category: 'REG', carrier: 'TRENITALIA', terminus: 'ORTE', time: '18:10', cancelled: true,
    stops: [fermata('ROMA TIBURTINA', '18:17'), fermata('FARA SABINA-MONTELIBRETTI', '18:44'), fermata('ORTE', '19:10')] },
  { number: '9638', category: "ALTA VELOCITA'", carrier: 'FRECCIAROSSA', terminus: 'MILANO CENTRALE', time: '18:15',
    platform: '8', live: 0,
    stops: [fermata('FIRENZE SANTA MARIA NOVELLA', '19:46'), fermata('BOLOGNA CENTRALE', '20:24'),
      fermata('MILANO CENTRALE', '21:30')] },
  { number: '4524', category: 'RV', carrier: 'TRENITALIA', terminus: 'FROSINONE', time: '18:20', platform: '18',
    stops: [fermata('ROMA TUSCOLANA', '18:26'), fermata('FROSINONE', '19:24')] },
];

function treno(t) {
  const fuori = {
    number: t.number, category: t.category, carrier: t.carrier, terminus: t.terminus, time: t.time,
    stops: t.stops,
  };
  if (t.delay) fuori.delay = t.delay;
  if (t.cancelled) fuori.cancelled = true;
  if (t.platform) fuori.platform = t.platform;
  if (t.boarding) fuori.boarding = true;
  if (t.notes) fuori.notes = t.notes;
  if (t.live !== undefined) fuori.liveDelay = t.live;
  if (t.platformScheduled) {
    Object.assign(fuori, { platformChanged: true, platformScheduled: t.platformScheduled, platformActual: t.platform });
  }
  return fuori;
}

// Due stazioni con poche righe ciascuna, per le tessere della home che non
// partono da Termini.
const PARTENZE = {
  [TERMINI]: { station: 'ROMA TERMINI', trains: PARTENZE_TERMINI },
  [TIBURTINA]: { station: 'ROMA TIBURTINA', trains: [
    { number: '4517', category: 'REG', carrier: 'TRENITALIA', terminus: 'ROMA TERMINI', time: '17:58', platform: '4', live: 1,
      stops: [fermata('ROMA TERMINI', '18:06')] },
    { number: '9583', category: "ALTA VELOCITA'", carrier: 'FRECCIAROSSA', terminus: 'NAPOLI CENTRALE', time: '18:08',
      delay: 4, live: 4, platform: '15', stops: [fermata('ROMA TERMINI', '18:16'), fermata('NAPOLI CENTRALE', '19:30')] },
  ] },
  [OSTIENSE]: { station: 'ROMA OSTIENSE', trains: [
    { number: '24811', category: 'REG', carrier: 'TRENITALIA', terminus: 'FIUMICINO AEROPORTO', time: '18:01', platform: '11',
      delay: 6, live: 6,
      stops: [fermata('ROMA TRASTEVERE', '18:05'), fermata('FIUMICINO AEROPORTO', '18:29')] },
  ] },
};

const catalogo = new Map(); // id -> nome come lo scrive RFI

function tabellone(url) {
  const q = new URL(url).searchParams;
  const da = Number(q.get('from'));
  const a = Number(q.get('to')) || 0;
  const s = PARTENZE[da] || { station: catalogo.get(da) || '', trains: [] };
  let treni = s.trains.map(treno);
  const fuori = { placeId: da, station: s.station, arrivals: false, filtered: !!a };
  if (a) {
    const dove = catalogo.get(a);
    treni = treni.filter((t) => t.stops.some((f) => f.name === dove))
      .map((t) => ({ ...t, arrival: t.stops.find((f) => f.name === dove).time }));
    Object.assign(fuori, { from: catalogo.get(da), to: dove, total: s.trains.length });
  }
  if (da === TERMINI && !a) fuori.notices = AVVISI_TERMINI;
  return { ...fuori, trains: treni };
}

const AVVISI_TERMINI = [
  'ASCENSORE DEL SOTTOPASSO LATO VIA MARSALA FUORI SERVIZIO. PER RAGGIUNGERE I BINARI 1-11 UTILIZZARE L\'ASCENSORE DI TESTATA.',
];

// Il Frecciarossa che si sta prendendo: viene da Milano, è passato da Bologna
// e Firenze perdendo qualche minuto, e a Roma Tiburtina ne ha sette.
const fermataV = (name, scheduled, delay, extra = {}) => {
  const f = { name, scheduled, ...extra };
  if (delay !== undefined) {
    const [h, m] = scheduled.split(':').map(Number);
    const t = h * 60 + m + delay;
    Object.assign(f, { passed: true, delay, actual: `${String(Math.floor(t / 60) % 24).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}` });
  }
  return f;
};

const VIAGGIO = {
  tracked: true,
  id: { origin: 'S01700', number: '9541', date: MEZZANOTTE },
  number: '9541', category: 'FR', origin: 'MILANO CENTRALE', terminus: 'NAPOLI CENTRALE',
  arrived: false, disrupted: false, delay: 7,
  lastSeen: { station: 'ROMA TIBURTINA', time: '17:50' },
  stops: [
    fermataV('MILANO CENTRALE', '14:45', 0, { platform: '14', lat: 45.486, lon: 9.205 }),
    fermataV('MILANO ROGOREDO', '14:55', 1, { platform: '4', lat: 45.433, lon: 9.239 }),
    fermataV('REGGIO EMILIA AV MEDIOPADANA', '15:26', 2, { platform: '2', lat: 44.723, lon: 10.588 }),
    fermataV('BOLOGNA CENTRALE', '15:51', 4, { platform: '17', lat: 44.506, lon: 11.342 }),
    fermataV('FIRENZE SANTA MARIA NOVELLA', '16:30', 9, { platform: '9', lat: 43.777, lon: 11.247 }),
    fermataV('ROMA TIBURTINA', '17:43', 7, { platform: '15', lat: 41.911, lon: 12.531 }),
    fermataV('ROMA TERMINI', '17:55', undefined, { platform: '12', boarding: true, lat: 41.901, lon: 12.502 }),
    fermataV('NAPOLI AFRAGOLA', '18:58', undefined, { platform: '3', lat: 40.931, lon: 14.331 }),
    fermataV('NAPOLI CENTRALE', '19:08', undefined, { platform: '19', chosen: true, lat: 40.853, lon: 14.273 }),
  ],
};

// L'Intercity per Milano aperto dalla tratta per Tiburtina: viene da Napoli,
// ha cambiato binario a Termini e ha qualche minuto da Formia in poi.
const VIAGGIO_IC = {
  tracked: true,
  id: { origin: 'S09218', number: '594', date: MEZZANOTTE },
  number: '594', category: 'IC', origin: 'NAPOLI CENTRALE', terminus: 'MILANO CENTRALE', delay: 4,
  lastSeen: { station: 'ROMA TUSCOLANA', time: '17:49' },
  stops: [
    fermataV('NAPOLI CENTRALE', '15:40', 0, { platform: '14' }),
    fermataV('AVERSA', '15:55', 1, { platform: '2' }),
    fermataV('FORMIA-GAETA', '16:35', 3, { platform: '1' }),
    fermataV('LATINA', '17:12', 5, { platform: '2' }),
    fermataV('ROMA TERMINI', '18:05', undefined, { platform: '9', platformScheduled: '6', boarding: true }),
    fermataV('ROMA TIBURTINA', '18:13', undefined, { platform: '11', chosen: true }),
    fermataV('ORVIETO', '19:11', undefined, { platform: '2' }),
    fermataV('CHIUSI-CHIANCIANO TERME', '19:35', undefined, { platform: '1' }),
    fermataV('FIRENZE SANTA MARIA NOVELLA', '20:40', undefined, { platform: '8' }),
    fermataV('BOLOGNA CENTRALE', '21:52', undefined, { platform: '5' }),
    fermataV('MILANO CENTRALE', '00:15', undefined, { platform: '12' }),
  ],
};

// Il regionale per Foligno: è ancora in stazione, quindi nessuna fermata è
// passata.
const VIAGGIO_REGIONALE = {
  tracked: true,
  id: { origin: 'S08409', number: '4520', date: MEZZANOTTE },
  number: '4520', category: 'REG', origin: 'ROMA TERMINI', terminus: 'FOLIGNO', delay: 0,
  lastSeen: { station: '', time: '' },
  stops: [
    { name: 'ROMA TERMINI', scheduled: '18:00', platform: '1EST', boarding: true },
    { name: 'ROMA TIBURTINA', scheduled: '18:07', platform: '6', chosen: true },
    { name: 'ORTE', scheduled: '18:45', platform: '2' },
    { name: 'NARNI-AMELIA', scheduled: '18:55', platform: '1' },
    { name: 'TERNI', scheduled: '19:05', platform: '3' },
    { name: 'SPOLETO', scheduled: '19:31', platform: '1' },
    { name: 'FOLIGNO', scheduled: '19:56', platform: '7' },
  ],
};

// Le linee Trenord sono inventate quanto il resto: nomi plausibili, nessuna
// delle linee del nord-ovest, e un solo sciopero.
const minuti = (m) => new Date(ADESSO.getTime() - m * 60_000).toISOString();
const LINEE = {
  updated: minuti(1),
  lines: [
    { code: 'S2', name: 'MARIANO COMENSE - MILANO ROGOREDO', group: 'LINEE SUBURBANE', status: 0 },
    { code: 'S12', name: 'MELEGNANO - CORMANO', group: 'LINEE SUBURBANE', status: 0 },
    { code: 'S13', name: 'MILANO BOVISA - PAVIA', group: 'LINEE SUBURBANE', status: 1, notices: [
      { date: minuti(25), section: 2, text: 'Il treno 24741 viaggia con 15 minuti di ritardo per un guasto a un passaggio a livello nei pressi di Locate Triulzi.' },
    ] },
    { code: 'RE_11', name: 'MILANO - BRESCIA - VERONA', group: 'REGIO EXPRESS', status: 0 },
    { code: 'RE_13', name: 'MILANO - PAVIA - VOGHERA', group: 'REGIO EXPRESS', status: 2, notices: [
      { date: minuti(240), section: 1, strike: true, text: 'Sciopero del personale dalle 21:00 di oggi alle 21:00 di domani. Sono garantiti i treni in partenza dalle 6:00 alle 9:00 e dalle 18:00 alle 21:00.' },
      { date: minuti(40), section: 2, text: 'Circolazione rallentata fra Pavia e Voghera per un controllo tecnico alla linea: ritardi fino a 20 minuti.' },
    ] },
    { code: 'R38', name: 'MILANO - LODI - PIACENZA', group: 'REGIONALI', status: 0 },
    { code: 'R39', name: 'MILANO - CREMONA - MANTOVA', group: 'REGIONALI', status: 0 },
    { code: 'R40', name: 'PAVIA - CODOGNO', group: 'REGIONALI', status: 1, notices: [
      { date: minuti(70), section: 2, text: 'Il treno 2671 è limitato a Casalpusterlengo per un guasto al materiale.' },
    ] },
  ],
};

/* ------------------------------------------------------------ le viste */

// Quello che il telefono ha salvato: due tratte di Roma, la partenza da
// Termini, la navetta per l'aeroporto, il treno che si sta prendendo e un
// treno abituale.
const SALVATO = {
  'tt.preferiti': [{ f: TERMINI, t: TIBURTINA }, { f: OSTIENSE, t: FIUMICINO }, { f: TERMINI }, { f: TIBURTINA, t: TERMINI }],
  'tt.campi': { da: TERMINI, a: NAPOLI },
  'tt.seguiti': [{ o: 'S01700', n: '9541', d: MEZZANOTTE, cat: 'FR', capolinea: 'NAPOLI CENTRALE', f: TERMINI, a: NAPOLI }],
  // Il treno abituale è quello della mattina: alle 17:52 non è fra i treni
  // del momento, e la home ne mostra la riga e non una seconda scheda.
  'tt.abituali': [{ o: 'S07217', n: '4507', f: TIBURTINA, at: '07:48', days: [1, 2, 3, 4, 5], cat: 'REG', capolinea: 'ROMA TERMINI' }],
  'tt.campanelle': ['RE_13', 'S13'],
  'tt.posizione': true,
  'tt.notifiche': [{ giorni: [1, 2, 3, 4, 5], da: '07:00', a: '09:00' }, { giorni: [1, 2, 3, 4, 5], da: '17:00', a: '19:30' }],
};

const attendi = (ms) => (p) => p.waitForTimeout(ms);

const VISTE = {
  home: { rotta: '#/', dopo: attendi(2500) },
  tratta: { rotta: `#/p/${TERMINI}/${TIBURTINA}`, dopo: async (p) => {
    await p.waitForTimeout(2000);
    await p.click('.tabella.tratta details[data-treno] summary').catch(() => {});
    await p.waitForTimeout(1500);
  } },
  tabellone: { rotta: `#/p/${TERMINI}`, dopo: attendi(2500) },
  treno: { rotta: `#/t/S01700/9541/${MEZZANOTTE}`, dopo: async (p) => {
    await p.waitForTimeout(2000);
    await p.click('.precedenti summary').catch(() => {});
    await p.waitForTimeout(500);
  } },
  linee: { rotta: '#/linee', dopo: attendi(2000) },
  notifiche: { rotta: '#/notifiche', dopo: attendi(2000) },
  mappa: { rotta: `#/t/S01700/9541/${MEZZANOTTE}`, dopo: async (p) => {
    await p.waitForTimeout(1500);
    await p.click('[data-mappa]');
    await p.waitForTimeout(4000);
  } },
  ricerca: { rotta: '#/', dopo: async (p) => {
    await p.waitForTimeout(1500);
    await p.click('[data-apri="a"]');
    await p.waitForTimeout(400);
    await p.fill('#cerca', 'centrale');
    await p.waitForTimeout(600);
  } },
};

async function apri(page, vista) {
  await page.clock.setFixedTime(ADESSO);
  // Chromium non ha aree sicure: env() vale zero, e il pannello della ricerca
  // finiva sotto l'indicatore invece di lasciargli la sua fascia come
  // sull'iPhone. Il foglio di stile arriva con i valori del telefono già
  // scritti; sopra resta zero, perché la barra di stato sta fuori dalla pagina.
  await page.route('**/app.css*', async (r) => {
    const css = await (await r.fetch()).text();
    r.fulfill({ contentType: 'text/css', body: css
      .replaceAll('env(safe-area-inset-bottom)', `${FONDO}px`)
      .replaceAll('env(safe-area-inset-top)', '0px') });
  });
  await page.route('**/api/board**', (r) => r.fulfill({ json: tabellone(r.request().url()) }));
  await page.route('**/api/train**', (r) => {
    const numero = new URL(r.request().url()).searchParams.get('number');
    const v = [VIAGGIO_IC, VIAGGIO_REGIONALE, VIAGGIO].find((x) => x.number === numero);
    r.fulfill({ json: v || { tracked: false } });
  });
  await page.route('**/api/journey**', (r) => {
    const numero = new URL(r.request().url()).searchParams.get('number');
    r.fulfill({ json: numero === VIAGGIO.number ? VIAGGIO : { tracked: false } });
  });
  await page.route('**/api/notices**', (r) => r.fulfill({ json: { stations: [{ placeId: TERMINI, station: 'ROMA TERMINI', notices: AVVISI_TERMINI }] } }));
  await page.route('**/api/lines/notices**', (r) => {
    const codice = new URL(r.request().url()).searchParams.get('line');
    r.fulfill({ json: { notices: (LINEE.lines.find((l) => l.code === codice) || {}).notices || [] } });
  });
  await page.route('**/api/lines', (r) => r.fulfill({ json: LINEE }));
  // Una chiave qualsiasi: vuota, l'app direbbe che le notifiche non sono
  // configurate, e nessuno si abbona davvero da qui.
  await page.route('**/api/push/key', (r) => r.fulfill({ json: { key: 'B' + 'A'.repeat(86) } }));
  await page.route('**/api/push/subscribe', (r) => r.fulfill({ status: 204 }));
  await page.addInitScript((salvato) => {
    for (const [k, v] of Object.entries(salvato)) localStorage.setItem(k, JSON.stringify(v));
    // Chromium senza testa risponde "denied" anche col permesso concesso, e
    // non sa abbonarsi a un servizio push: il telefono qui è già abbonato.
    if (window.Notification) Object.defineProperty(Notification, 'permission', { get: () => 'granted' });
    if (window.PushManager) {
      PushManager.prototype.getSubscription = async () => ({ endpoint: 'https://push.invalid/', toJSON: () => ({ endpoint: 'https://push.invalid/' }) });
    }
  }, SALVATO);
  await page.goto('http://localhost:18080/' + VISTE[vista].rotta);
  await page.waitForLoadState('networkidle');
  await VISTE[vista].dopo(page);
}

/* ------------------------------------------------------------ il telefono */

/* Il telefono è disegnato qui, non preso da un kit: titanio, il vetro nero
   attorno allo schermo, l'isola in alto e i tasti sul fianco. La barra di
   stato ha il colore dell'intestazione dell'app, come la tiene iOS per una
   pagina installata. Il telefono scuro è per il tema scuro, quello argento
   per il chiaro. */
const COLORI = {
  light: { testo: '#000000', tasto: '#c3c8d0', metallo: 'linear-gradient(135deg,#e9ebef,#b9bec7 45%,#dcdfe5)' },
  dark: { testo: '#ffffff', tasto: '#2a303b', metallo: 'linear-gradient(135deg,#3a4250,#1c2029 45%,#2c333f)' },
};

function telefono({ png, tema, fondo }) {
  const { testo, tasto, metallo } = COLORI[tema];
  return `<div class="telefono" style="background:${metallo};--testo:${testo};--tasto:${tasto};--fondo:${fondo}">
  <div class="lato" style="left:-3px;top:150px;height:30px"></div>
  <div class="lato" style="left:-3px;top:210px;height:58px"></div>
  <div class="lato" style="left:-3px;top:280px;height:58px"></div>
  <div class="lato" style="right:-3px;top:230px;height:92px"></div>
  <div class="lato" style="right:-3px;top:640px;height:56px"></div>
  <div class="vetro"></div>
  <div class="schermo">
    <img src="data:image/png;base64,${png.toString('base64')}">
    <div class="stato">
      <span class="ora">${ORA}</span>
      <span class="icone">
        <svg width="18" height="12" viewBox="0 0 18 12"><rect x="0" y="8" width="3" height="4" rx="1"/><rect x="5" y="5.5" width="3" height="6.5" rx="1"/><rect x="10" y="3" width="3" height="9" rx="1"/><rect x="15" y="0" width="3" height="12" rx="1"/></svg>
        <svg width="16" height="12" viewBox="0 0 16 12"><path d="M8 2.3c2.3 0 4.4.9 6 2.4l1.3-1.3A10.3 10.3 0 0 0 8 .4 10.3 10.3 0 0 0 .7 3.4L2 4.7a8.5 8.5 0 0 1 6-2.4Zm0 3.6c1.3 0 2.5.5 3.4 1.3l1.3-1.3A6.6 6.6 0 0 0 8 4a6.6 6.6 0 0 0-4.7 1.9l1.3 1.3C5.5 6.4 6.7 5.9 8 5.9Zm0 3.5c.4 0 .8.2 1.1.4L8 11.6 6.9 9.8c.3-.2.7-.4 1.1-.4Z"/></svg>
        <svg width="27" height="13" viewBox="0 0 27 13"><rect x=".5" y=".5" width="23" height="12" rx="3.5" fill="none" stroke="currentColor" stroke-opacity=".4"/><rect x="2" y="2" width="16" height="9" rx="2"/><path d="M25 4.5v4a2 2 0 0 0 0-4Z" fill-opacity=".45"/></svg>
      </span>
      <div class="isola"></div>
    </div>
    <div class="indicatore"></div>
  </div>
</div>`;
}

// Uno o più telefoni in fila, su fondo trasparente: la copertina ne ha due.
const cornice = (telefoni) => `<!doctype html><html><head><style>
  html, body { margin: 0; background: transparent; }
  body { padding: 40px 48px; display: inline-flex; gap: 56px; }
  .telefono { position: relative; flex: none; width: ${SCHERMO.width}px; height: ${SCHERMO.height}px; padding: 11px;
    border-radius: 70px; box-shadow: 0 30px 60px -20px rgba(0,0,0,.45), 0 12px 24px -12px rgba(0,0,0,.35); }
  .vetro { position: absolute; inset: 4px; border-radius: 66px; background: #050608; }
  .schermo { position: relative; width: 100%; height: 100%; border-radius: 58px; overflow: hidden; background: var(--fondo); }
  .schermo img { position: absolute; top: ${BARRA}px; left: 0; width: ${SCHERMO.width}px; display: block; }
  .stato { position: absolute; top: 0; left: 0; right: 0; height: ${BARRA}px; background: var(--fondo); color: var(--testo);
    font: 600 17px/1 "Liberation Sans", "Helvetica Neue", Arial, sans-serif; }
  .stato svg { fill: currentColor; }
  .ora { position: absolute; left: 0; width: 140px; top: 21px; text-align: center; letter-spacing: -.2px; }
  .icone { position: absolute; right: 0; width: 140px; top: 19px; display: flex; gap: 6px; justify-content: center; align-items: center; }
  .isola { position: absolute; top: 11px; left: 50%; transform: translateX(-50%); width: 108px; height: 32px;
    border-radius: 16px; background: #000; }
  .indicatore { position: absolute; bottom: 8px; left: 50%; transform: translateX(-50%); width: 128px; height: 4px;
    border-radius: 3px; background: var(--testo); opacity: .3; }
  .lato { position: absolute; width: 4px; border-radius: 2px; background: var(--tasto); }
</style></head><body>${telefoni.map(telefono).join('')}</body></html>`;

/* Fotografa i telefoni e salva in WebP. Un PNG con la mappa dentro pesava
   800 KB, e a ogni giro di schermate il repo se li porta dietro tutti: il
   WebP con la trasparenza sta sotto i cento, e GitHub lo mostra. Lo codifica
   il browser stesso, che lo sa già fare, invece di un programma in più. */
async function scatta(b, telefoni, file) {
  const ctx = await b.newContext({ deviceScaleFactor: SCALA });
  const c = await ctx.newPage();
  await c.setContent(cornice(telefoni));
  const png = await c.locator('body').screenshot({ omitBackground: true });
  const webp = await c.evaluate(async (b64) => {
    const img = new Image();
    img.src = 'data:image/png;base64,' + b64;
    await img.decode();
    const tela = Object.assign(document.createElement('canvas'), { width: img.width, height: img.height });
    tela.getContext('2d').drawImage(img, 0, 0);
    return tela.toDataURL('image/webp', 0.9).split(',')[1];
  }, png.toString('base64'));
  fs.writeFileSync(file, Buffer.from(webp, 'base64'));
  await ctx.close();
}

if (require.main === module) (async () => {
  const { chromium } = require(process.env.PW || 'playwright');
  const out = process.argv[2] || path.join(__dirname, '..', 'docs');
  // La lingua del browser, non del contesto, decide come si scrive un
  // <input type="time">: senza, le fasce uscivano "05:00 PM".
  const b = await chromium.launch({ args: ['--lang=it-IT'], env: { ...process.env, LANG: 'it_IT.UTF-8', LANGUAGE: 'it' } });

  // Il catalogo serve a dare un nome alle stazioni delle tratte finte.
  const r = await fetch('http://localhost:18080/api/stations');
  for (const [id, nome] of (await r.json()).stations) catalogo.set(id, nome);

  const solo = process.argv.slice(3);
  const home = {};
  for (const vista of Object.keys(VISTE)) {
    if (solo.length && !solo.includes(vista)) continue;
    for (const tema of ['light', 'dark']) {
      const ctx = await b.newContext({
        viewport: { width: SCHERMO.width, height: SCHERMO.height - BARRA },
        deviceScaleFactor: SCALA, colorScheme: tema, hasTouch: true, isMobile: true,
        locale: 'it-IT', timezoneId: 'Europe/Rome',
        // Notifiche e posizione concesse, come su un telefono già configurato:
        // altrimenti le viste si aprono con l'avviso di un permesso negato. La
        // posizione è davanti a Termini.
        permissions: ['notifications', 'geolocation'],
        geolocation: { latitude: 41.9009, longitude: 12.5016 },
      });
      const page = await ctx.newPage();
      page.on('pageerror', (e) => console.log(`ERRORE ${vista} ${tema} ${e.message}`));
      await apri(page, vista);
      const schermo = {
        png: await page.screenshot(), tema,
        fondo: await page.evaluate(() => getComputedStyle(document.querySelector('#testa')).backgroundColor),
      };
      await ctx.close();
      // La home va solo nella copertina, gli altri ognuno nel suo telefono.
      if (vista === 'home') home[tema] = schermo;
      else await scatta(b, [schermo], path.join(out, `${vista}-${tema === 'dark' ? 'scuro' : 'chiaro'}.webp`));
      console.log(`OK ${vista} ${tema}`);
    }
  }
  // La copertina: la stessa home nei due temi, una accanto all'altra.
  if (home.light) await scatta(b, [home.light, home.dark], path.join(out, 'copertina.webp'));
  await b.close();
})();

module.exports = { VISTE, apri, cornice };
