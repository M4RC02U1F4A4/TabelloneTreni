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
  // Il viaggio di un treno seguito si chiede con le coordinate di
  // ViaggiaTreno invece che con il tabellone: chi segue un treno lo guarda
  // quasi sempre quando ci è già sopra, e a quel punto un tabellone da cui
  // ricavarle non c'è più.
  // `to` è la stazione dove si scende, quando la si sa: il server la marca fra
  // le fermate e la scheda la accende. È lo stesso parametro del tabellone.
  viaggio: (t) =>
    `api/journey?origin=${encodeURIComponent(t.o)}&number=${encodeURIComponent(t.n)}&date=${t.d}`
    + (t.a ? `&to=${encodeURIComponent(t.a)}` : ''),
  linee: 'api/lines',
  avvisiLinea: (codice) => `api/lines/notices?line=${encodeURIComponent(codice)}`,
  avvisiStazione: (id) => `api/notices?stations=${id.join(',')}`,
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
  // Gli avvisi delle stazioni preferite, come li manda il server: solo quelle
  // che ne hanno almeno uno. Come le linee, non sono mai un motivo per non
  // disegnare la home — se non arrivano, il banner non c'è e nessuno lo sa.
  avvisiStazione: [],
};

/* Le schede aperte e i viaggi già scaricati.

   Il tabellone si ridisegna intero una volta al minuto: senza tenere da parte
   quali schede erano aperte, ogni aggiornamento le richiuderebbe in faccia a
   chi le stava leggendo. I viaggi restano in mano al client per lo stesso
   motivo — un ridisegno non deve rifare le richieste già fatte. */
const aperti = new Set();
const viaggi = new Map(); // numero treno -> { stato: 'attesa'|'ok'|'errore', dati }

/* I viaggi dei treni seguiti, indicizzati sulle coordinate invece che sul
   numero: lo stesso numero torna ogni giorno, e in home possono convivere il
   treno di stasera e quello di domattina. */
const viaggiSeguiti = new Map(); // "origine|numero|giorno" -> { stato, dati }

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
// Il banner degli avvisi di stazione, aperto o chiuso. Vale come lineeAperte:
// la home si ridisegna una volta al minuto, e senza ricordarselo il banner si
// richiuderebbe in faccia a chi stava leggendo l'avviso per intero.
let avvisiStazioneAperti = false;
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
  // Il segnalibro dei treni seguiti. Non è la stella dei preferiti né la
  // campana delle linee, e la differenza è vera: una tratta salvata resta lì
  // per sempre, una linea seguita manda notifiche, un treno seguito è una cosa
  // di stasera che si toglie da sola quando il treno arriva.
  segnalibro: '<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>',
  // La campana e il suo battaglio sono due tracciati separati: da piena, il
  // riempimento deve prendere la campana e lasciare fuori il battaglio,
  // altrimenti sotto il bordo compare una macchia che a 15px sembra sporco.
  orologio: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
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

/* Le fasce in cui si vogliono ricevere le notifiche sulle linee.

   Elenco vuoto vuol dire sempre, che è come stavano le cose prima: un guasto
   arriva quando succede, a qualunque ora. Con una fascia sopra, invece, fuori
   dagli orari non si perde niente — quello che è ancora in corso quando la
   fascia si apre arriva in quel momento. La regola sta nel servizio, che è il
   solo posto che sa com'è la linea adesso e cosa ti ha già raccontato.

   I giorni seguono la convenzione di time.Weekday, con la domenica a zero: sono
   quelli che il server si aspetta, e tradurli qui in mezzo vorrebbe dire avere
   due convenzioni e un punto in cui sbagliare. */
const fasce = () => leggi('tt.notifiche', []);

// Il nome IANA del fuso del telefono. Se il browser non lo dice — non capita
// più da anni, ma costa una riga — resta vuoto e il server usa l'ora italiana.
function fusoDelTelefono() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ''; }
  catch { return ''; }
}
const scriviFasce = (f) => scrivi('tt.notifiche', f);

// I giorni nell'ordine in cui si leggono in Italia: la settimana comincia il
// lunedì, anche se nel modello la domenica è zero.
const GIORNI = [
  { v: 1, l: 'L' }, { v: 2, l: 'M' }, { v: 3, l: 'M' }, { v: 4, l: 'G' },
  { v: 5, l: 'V' }, { v: 6, l: 'S' }, { v: 0, l: 'D' },
];

// Andata al lavoro e ritorno: la prima fascia che si aggiunge è quasi sempre
// una delle due, e proporla già scritta risparmia quattro tocchi.
const FASCE_PROPOSTE = [
  { giorni: [1, 2, 3, 4, 5], da: '07:00', a: '09:00' },
  { giorni: [1, 2, 3, 4, 5], da: '17:00', a: '19:00' },
];

// Una fascia con gli estremi uguali non è né vuota né di un giorno intero: è
// una fascia che chi la stava scrivendo non ha finito. Il server la rifiuta, e
// mandarla vorrebbe dire far comparire un errore al posto di una spiegazione.
const fasciaCompleta = (f) => !!f.da && !!f.a && f.da !== f.a;
const fasceValide = () => fasce().every(fasciaCompleta);
const seguita = (codice) => campanelle().includes(codice);

function alternaCampanella(codice) {
  const elenco = campanelle().filter((c) => c !== codice);
  if (elenco.length === campanelle().length) elenco.unshift(codice);
  scrivi('tt.campanelle', elenco);
}

/* I treni seguiti.

   Si salvano le tre coordinate di ViaggiaTreno — stazione di origine, numero,
   giorno di partenza — perché sono l'unico modo per richiedere il viaggio senza
   un tabellone sotto, ed è proprio senza tabellone che serve: un treno seguito
   lo si guarda da sopra il treno, quando dalle partenze della stazione da cui
   è partito è sparito da un pezzo.

   Insieme va l'etichetta con cui il tabellone lo chiamava, così la scheda in
   home sa già dire di che treno si tratta prima che la rete risponda. */
const seguiti = () => leggi('tt.seguiti', []);
const chiaveTreno = (t) => `${t.o}|${t.n}|${t.d}`;
const eSeguito = (t) => seguiti().some((x) => chiaveTreno(x) === chiaveTreno(t));

function alternaSeguito(t) {
  const k = chiaveTreno(t);
  const elenco = seguiti().filter((x) => chiaveTreno(x) !== k);
  if (elenco.length === seguiti().length) elenco.push(t);
  scrivi('tt.seguiti', elenco);
}

/* Togliere il segnalibro non butta via il viaggio già scaricato: si può essere
   fermi sulla sua scheda, e vederla svuotarsi sotto le dita sarebbe la risposta
   tolta di mano proprio a chi la stava leggendo. La mappa vive quanto la
   pagina; l'elenco che conta è quello salvato. */
const smettiDiSeguire = (k) => {
  scrivi('tt.seguiti', seguiti().filter((x) => chiaveTreno(x) !== k));
  dimenticaViaggio(k);
};

/* L'ultima lettura di ogni treno seguito, su disco accanto al segnalibro.

   Il service worker non mette in cache i dati vivi, e per un tabellone è
   giusto: uno vecchio è peggio di nessuno. Ma un treno seguito lo si guarda in
   galleria, dove la rete non c'è ed è esattamente lì che il dato servirebbe —
   e riaprendo l'app si trovava "cerco dov'è il treno…" e poi un errore, cioè
   niente, al posto di una posizione di quattro minuti prima. L'app già fa
   questa eccezione: su errore tiene l'ultima lettura buona invece di svuotare
   la scheda. Questa la fa sopravvivere anche alla chiusura, con la sua età
   scritta accanto — un dato vecchio dichiarato vecchio è un dato.

   Un viaggio arrivato non si scrive mai: sarebbe l'unica cosa che riaprendo
   l'app resusciterebbe un treno già finito. */
const viaggiSalvati = () => leggi('tt.viaggi', {});

function ricordaViaggio(k, dati) {
  if (dati.arrived) return;
  const m = viaggiSalvati();
  m[k] = { lettoIl: Date.now(), dati };
  scrivi('tt.viaggi', m);
}

function dimenticaViaggio(k) {
  const m = viaggiSalvati();
  if (!(k in m)) return;
  delete m[k];
  scrivi('tt.viaggi', m);
}

/* Rimette in memoria quello che c'era, all'avvio, prima che la rete risponda:
   è tutto il senso di averlo salvato. Solo per i treni ancora seguiti — un
   viaggio senza più segnalibro non ha una scheda in cui comparire. */
function idrataViaggi() {
  const m = viaggiSalvati();
  const vivi = new Set(seguiti().map(chiaveTreno));
  let potato = false;
  for (const [k, v] of Object.entries(m)) {
    if (!vivi.has(k) || !v || !v.dati) { delete m[k]; potato = true; continue; }
    viaggiSeguiti.set(k, { stato: 'ok', dati: v.dati, lettoIl: v.lettoIl });
  }
  if (potato) scrivi('tt.viaggi', m);
}

// Le tre coordinate più l'etichetta con cui chiamarlo prima che la rete
// risponda, presa dal viaggio stesso e non dal tabellone: così un treno seguito
// si descrive da sé anche riaprendo l'app il giorno dopo.
//
// E dove si scende, che è l'unica cosa che il tabellone sapeva di chi guarda e
// non del treno. Seguendo la si buttava via: il filtro sapeva che andavi a
// Gallarate, la scheda seguita non più. Salvata, il server marca quella fermata
// fra le altre e la lista la accende — la stessa `chosen` che il tabellone usa
// già, senza niente di nuovo di là.
const daSeguire = (d, a) => ({
  o: d.id.origin, n: d.id.number, d: d.id.date,
  cat: d.category, capolinea: d.terminus,
  ...(a ? { a } : {}),
});

/* Un treno seguito si toglie da sé, in due momenti.

   Il primo lo dice il server con `ended`, mezz'ora dopo l'arrivo: prima no,
   perché chi seguiva il treno è lì per vedere proprio che è arrivato, e una
   scheda che sparisce nell'istante giusto è una risposta tolta di mano.

   Il secondo è questo, ed è la rete di sicurezza per i viaggi di cui il server
   non dirà mai più niente — ViaggiaTreno si dimentica i treni di ieri. Il
   giorno di partenza è la mezzanotte di quel giorno, e un treno che parte alle
   23:50 arriva il giorno dopo: trentasei ore coprono anche quello. */
const DURATA_SEGUITO = 36 * 60 * 60_000;

function potaSeguiti() {
  const vivi = seguiti().filter((t) => Date.now() - t.d < DURATA_SEGUITO);
  if (vivi.length !== seguiti().length) scrivi('tt.seguiti', vivi);
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

/* Il viaggio di un treno seguito.

   Un tentativo andato bene non si butta per uno andato male: la rete di un
   treno in corsa cade a tratti, e l'ultima posizione nota è più utile di una
   scheda che si svuota ogni volta che si entra in galleria. */
async function caricaViaggioSeguito(t, forza) {
  const k = chiaveTreno(t);
  const gia = viaggiSeguiti.get(k);
  if (!forza && gia && gia.stato !== 'errore') return;
  if (!gia) viaggiSeguiti.set(k, { stato: 'attesa' });
  // La destinazione la sa solo il segnalibro: nella rotta `#/t/o/n/d` ci sono
  // le tre coordinate e basta, e un viaggio chiesto da lì tornava senza la
  // fermata accesa. Si risolve qui, che è l'unico punto per cui passano tutte
  // le letture — dalla home e dalla scheda aperta.
  const salvato = seguiti().find((x) => chiaveTreno(x) === k);
  const chiesto = t.a ? t : { ...t, a: salvato && salvato.a };
  try {
    const r = await fetch(API.viaggio(chiesto), { signal: AbortSignal.timeout(15_000) });
    if (controllaVersione(r)) return;
    if (!r.ok) throw new Error(`errore ${r.status}`);
    const d = await r.json();
    viaggiSeguiti.set(k, { stato: 'ok', dati: d, lettoIl: Date.now() });
    ricordaViaggio(k, d);
    // Arrivato, il segnalibro si toglie subito — ma il viaggio resta in mano
    // alla pagina: chi in quel momento lo sta guardando vede che il treno è
    // arrivato, che è la cosa per cui lo seguiva. È la stessa separazione di
    // sempre, fra l'elenco salvato e la copia che la pagina ha già in mano; a
    // sparire è solo la mezz'ora di attesa, che teneva in lista un viaggio
    // finito per nessuno.
    if (d.arrived) smettiDiSeguire(k);
  } catch {
    // L'ultima lettura buona resta, in memoria e su disco: su un treno la rete
    // cade a tratti, e la posizione di un minuto fa vale più di una riga vuota.
    if (!gia || gia.stato !== 'ok') viaggiSeguiti.set(k, { stato: 'errore' });
  }
}

/* Tutti i treni seguiti insieme. Sono pochi per definizione — si seguono i
   treni che si prendono — e partono in parallelo: in fila la home aspetterebbe
   la somma di altrettante letture su un servizio lento. */
async function aggiornaSeguiti(forza) {
  potaSeguiti();
  const elenco = seguiti();
  if (!elenco.length) return;
  await Promise.all(elenco.map((t) => caricaViaggioSeguito(t, forza)));
  stato.scaricatoIl = Date.now();
}

/* Gli avvisi delle stazioni preferite: gli ascensori guasti, i lavori che per
   tre mesi spostano i treni. Si chiedono per le sole stazioni di partenza dei
   preferiti, distinte — è l'unico posto in cui il banner compare, e senza
   preferiti non c'è niente da chiedere.

   Vale la regola delle linee, in forma più severa: qui non si mostra nemmeno
   il motivo dell'errore. Un banner giallo in cima alla home che dice che il
   banner giallo non funziona è peggio del banner che manca. */
async function caricaAvvisiStazione() {
  // Otto è il tetto che accetta il server: più preferiti di così sulla stessa
  // schermata non ci stanno, e sarebbero otto pagine di RFI per una striscia.
  const ids = [...new Set(preferiti().map((p) => p.f))].slice(0, 8);
  if (!ids.length) { stato.avvisiStazione = []; return; }
  try {
    const r = await fetch(API.avvisiStazione(ids));
    if (controllaVersione(r)) return;
    if (!r.ok) throw new Error(`errore ${r.status}`);
    stato.avvisiStazione = (await r.json()).stations || [];
  } catch {
    stato.avvisiStazione = [];
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
  // Una fascia a metà non si manda: il server la rifiuterebbe, e chi la stava
  // scrivendo vedrebbe un errore invece della riga che gli dice cosa manca.
  if (!fasceValide()) {
    notificheErrore = null;
    aggiornaVista();
    return;
  }
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
      // Il fuso lo dichiara il telefono: le fasce sono orari sul suo
      // quadrante, e leggerle su quello del server vorrebbe dire notifiche a
      // ore che non c'entrano niente con quelle scritte.
      body: JSON.stringify({
        subscription: abbonamento,
        lines: linee,
        windows: fasce().filter(fasciaCompleta).map((f) => ({
          days: f.giorni, from: f.da, to: f.a,
        })),
        timezone: fusoDelTelefono(),
      }),
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
  if (parti[0] === 'notifiche') return { vista: 'notifiche' };
  if (parti[0] === 'linee') return { vista: 'linee', filtro: decodeURIComponent(parti[1] || '') };
  // #/t/S01700/2247/1788645600000 è un treno seguito. Nella rotta ci sono le
  // stesse tre coordinate che si mandano al server, e non il tabellone da cui
  // il treno veniva: quel tabellone, quando lo si riapre, non lo porta più.
  if (parti[0] === 't') {
    const t = {
      o: decodeURIComponent(parti[1] || ''),
      n: decodeURIComponent(parti[2] || ''),
      d: Number(parti[3]),
    };
    return t.o && t.n && t.d ? { vista: 'treno', treno: t } : { vista: 'home' };
  }
  if (parti[0] !== 'p' && parti[0] !== 'a') return { vista: 'home' };
  const da = Number(parti[1]);
  if (!da) return { vista: 'home' };
  return { vista: 'risultati', da, a: Number(parti[2]) || null, arrivi: parti[0] === 'a' };
}

const rottaDi = (da, a, arrivi) => `#/${arrivi ? 'a' : 'p'}/${da}` + (a && !arrivi ? `/${a}` : '');
const ROTTA_LINEE = '#/linee';
const ROTTA_NOTIFICHE = '#/notifiche';
const rottaTreno = (t) =>
  `#/t/${encodeURIComponent(t.o)}/${encodeURIComponent(t.n)}/${t.d}`;

function vaiAiRisultati() {
  if (!stato.da) return;
  location.hash = rottaDi(stato.da, stato.a, stato.arrivi);
}

/* Il codice arriva da una notifica, cioè da fuori: si confronta normalizzato e
   senza spazi come fa il filtro, così `RE_13`, `re13` e `RE 13` finiscono tutti
   sulla stessa riga. Uno che non esiste più non apre niente e resta il filtro. */
function apriLineaDaRotta(codice) {
  const q = canon(codice).replace(/ /g, '');
  const l = (stato.linee || []).find((x) => canon(x.code).replace(/ /g, '') === q);
  if (!l) return;
  lineeAperte.add(l.code);
  scaricaAvvisi(l.code);
}

async function cambiaRotta() {
  const r = leggiRotta();
  fermaTimer();

  if (r.vista === 'notifiche') {
    disegna();
    return;
  }

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
    if (leggiRotta().vista !== 'linee') return;
    // Chi arriva dalla notifica ha già scelto la linea: lasciare la riga chiusa
    // gli chiederebbe un tocco in più per leggere il motivo per cui l'ha
    // toccata. Va fatto qui, dopo il clear() qui sopra e dopo caricaLinee(),
    // perché il codice della rotta va confrontato con quelli veri.
    if (r.filtro) apriLineaDaRotta(r.filtro);
    disegna();
    return;
  }

  if (r.vista === 'treno') {
    stato.errore = null;
    disegna();
    await caricaViaggioSeguito(r.treno, true);
    if (leggiRotta().vista !== 'treno') return;
    stato.scaricatoIl = Date.now();
    disegna();
    avviaTimer(() => caricaViaggioSeguito(r.treno, true).then(() => {
      if (leggiRotta().vista === 'treno') { stato.scaricatoIl = Date.now(); disegna(); }
    }));
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
    // Le schede dei treni seguiti si disegnano subito con quello che c'è già,
    // e la posizione ci si appoggia quando arriva: come i bollini, non devono
    // far aspettare la home.
    potaSeguiti();
    disegna();
    if (seguiti().length) {
      // Un treno in corsa si muove: la home smette di essere una pagina ferma
      // e si aggiorna al minuto come un tabellone, ma solo quando c'è qualcosa
      // da aggiornare.
      avviaTimer(() => aggiornaSeguiti(true).then(() => {
        if (leggiRotta().vista === 'home') disegna();
      }));
      aggiornaSeguiti(true).then(() => { if (leggiRotta().vista === 'home') disegna(); });
    }
    // I bollini arrivano da un secondo servizio e non devono far aspettare la
    // home: si ridisegna quando ci sono, e solo se nel frattempo non si è
    // andati altrove.
    Promise.all([caricaLinee(), caricaAvvisiStazione(), chiaveNotifiche().catch(() => {})])
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
  avviaTimer(caricaTabellone);
}

/* ------------------------------------------------------------------ timer */

/* Il ritmo è lo stesso ovunque — una volta al minuto, e fermo quando la pagina
   non è in primo piano — mentre cosa si rilegge cambia da vista a vista: un
   tabellone, un treno seguito, la manciata di treni in cima alla home. */
function avviaTimer(rinfresca) {
  fermaTimer();
  timerRinfresco = setInterval(() => {
    if (document.visibilityState === 'visible') rinfresca();
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
    // Gli avvisi di stazione stanno solo in home. Il server li tiene dieci
    // minuti, quindi rifarne la richiesta a ogni ritorno sull'app costa una
    // 304 e non una pagina di RFI.
    const attese = [caricaLinee()];
    if (vista === 'home') attese.push(caricaAvvisiStazione());
    Promise.all(attese).then(() => { if (leggiRotta().vista === vista) disegna(); });
    // Un treno seguito è la cosa che invecchia più in fretta di tutte: chi
    // riapre l'app dopo dieci minuti vuole sapere dov'è adesso, non dov'era
    // quando l'ha chiusa.
    if (vista === 'home') {
      aggiornaSeguiti(true).then(() => { if (leggiRotta().vista === 'home') disegna(); });
    }
    return;
  }

  if (vista === 'treno') {
    const t = leggiRotta().treno;
    caricaViaggioSeguito(t, true).then(() => {
      if (leggiRotta().vista === 'treno') { stato.scaricatoIl = Date.now(); disegna(); }
    });
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
  else if (r.vista === 'notifiche') disegnaNotifiche();
  else if (r.vista === 'linee') disegnaLinee();
  else if (r.vista === 'treno') disegnaTreno(r.treno);
  else disegnaRisultati();
}

function disegnaHome() {
  testa.innerHTML = `
    <div class="testa-riga"><h1 class="titolo">Tabellone Treni</h1></div>
    <div class="sottotitolo">Partenze e arrivi RFI, filtrati per dove devi andare</div>`;

  const fav = preferiti();
  if (!fav.length) modificaPreferiti = false;

  app.innerHTML = `
    ${bannerAvvisi()}
    ${sezioneSeguiti()}
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
      <div class="coppia">
        <button class="tessera" type="button" data-apri="partenze">
          ${icona('su')}<span>Partenze</span>
        </button>
        <button class="tessera" type="button" data-apri="arrivi">
          ${icona('giu')}<span>Arrivi</span>
        </button>
      </div>
    </section>

    ${sezioneLinee()}`;
}

/* I treni seguiti stanno sopra a tutto il resto, ed è l'unica sezione che si
   muove da sola.

   Sopra le tratte salvate perché non sono la stessa cosa: una tratta è un
   posto dove si va spesso, un treno seguito è quello su cui si è adesso, e fra
   le due chi apre l'app di corsa cerca la seconda. Nei giorni in cui non se ne
   segue nessuno la sezione non c'è, e la home è quella di prima. */
function sezioneSeguiti() {
  const elenco = seguiti();
  if (!elenco.length) return '';
  return `<section class="sezione">
    <div class="testa-sezione"><h2 class="etichetta-sezione">Treni seguiti</h2></div>
    <ul>${elenco.map(schedaSeguito).join('')}</ul>
  </section>`;
}

const etichettaTreno = (d) => {
  const nome = esc([d.category, d.number].filter(Boolean).join(' ')) || 'treno';
  return d.terminus ? `${nome} <span class="freccia">→</span> ${esc(d.terminus)}` : nome;
};

/* La prima fermata non ancora servita: è dove il treno sta andando adesso.
   Si guarda l'orario reale delle fermate e non l'ultimo rilevamento, che può
   benissimo essere un posto in cui il treno non ferma. */
const prossimaFermata = (d) => (d.stops || []).find((f) => !f.passed) || null;

/* Il numero grande della scheda, che è il ritardo — ma solo quando un ritardo
   esiste.

   Finché il treno non è stato rilevato da nessuna parte non esiste: è la stessa
   regola della pastiglia ciano sul tabellone, e uno zero al suo posto sarebbe
   una puntualità che nessuno ha visto. Lì al posto del ritardo va l'ora a cui
   deve partire, che è l'unica cosa vera che si sappia di lui; a viaggio finito
   va l'ora a cui è arrivato davvero, che è il motivo per cui lo si seguiva. */
function numeroGrande(d) {
  const fermate = d.stops || [];
  if (d.arrived) {
    const fine = fermate[fermate.length - 1] || {};
    return { testo: fine.actual || fine.scheduled || '–', cap: 'arrivato' };
  }
  if (!d.tracked) {
    const partenza = fermate[0] && fermate[0].scheduled;
    return partenza ? { testo: partenza, cap: 'parte' } : { testo: '–', cap: '' };
  }
  const min = d.delay || 0;
  return { testo: segnoRitardo(min), cap: 'ritardo', inRitardo: min > 0 };
}

/* Dov'è adesso, detto per esteso. Sta su una riga sua, sotto tutto il resto:
   è una frase, non un dato incolonnato, e spezzata in mezzo agli altri campi
   si leggerebbe peggio. */
function doveAdesso(d, lettoIl) {
  if (d.arrived) return d.terminus ? `arrivato a ${esc(d.terminus)}` : 'arrivato';
  if (!d.tracked) return 'non ancora partito';
  const l = d.lastSeen || {};
  if (!l.station) return 'non ancora partito';
  return `rilevato a ${esc(l.station)}${l.time ? ` alle ${esc(l.time)}` : ''}${etaLettura(lettoIl)}`;
}

/* Quanto è vecchia questa lettura, ma solo quando è vecchia. Fresca non si
   dice: "letto adesso" su ogni scheda sarebbe una riga in più che non informa
   nessuno. Serve nei due casi in cui la scheda mostra un dato che non è di
   adesso — riaperta senza rete, o con l'ultimo aggiornamento andato male — e
   sono proprio quelli in cui tacere farebbe passare il vecchio per nuovo. */
const VECCHIA = 2 * 60_000;

function etaLettura(lettoIl) {
  if (!lettoIl || Date.now() - lettoIl < VECCHIA) return '';
  const m = Math.round((Date.now() - lettoIl) / 60_000);
  return ` · letto ${m === 1 ? 'un minuto' : `${m} minuti`} fa`;
}

/* Il provvedimento, quando c'è. Sta in cima alla scheda perché cambia il senso
   di tutto quello che c'è sotto: un +5 su un treno soppresso è la bugia
   peggiore che questa scheda possa raccontare, e non si corregge stampandolo
   più in piccolo.

   Non dice quale provvedimento sia, perché il server non lo sa: ViaggiaTreno
   manda un numero non documentato, e "soppresso" scritto su un codice mai
   visto sarebbe un'invenzione con l'aria di un dato. Dice che c'è, che è la
   parte vera, e quello che si sa in più — le fermate dichiarate soppresse —
   glielo si mette accanto. */
function fasciaProvvedimento(d) {
  if (!d.disrupted) return '';
  const n = d.suppressedStops;
  return `<p class="provvedimento">provvedimento su questo treno${
    n ? ` · ${n} ${n === 1 ? 'fermata soppressa' : 'fermate soppresse'}` : ''}</p>`;
}

/* Il corpo della scheda di un treno seguito: le tre cose che si vogliono
   sapere da sopra il treno — di quanto è in ritardo, dov'è adesso, e a che
   binario arriva alla prossima fermata. Sono le stesse in home e sulla scheda
   aperta, quindi le compone una funzione sola. */
function corpoSeguito(d, lettoIl) {
  const n = numeroGrande(d);
  const f = prossimaFermata(d);
  const cambio = f && f.platformScheduled ? `era ${f.platformScheduled}` : '';
  return `
    ${fasciaProvvedimento(d)}
    <div class="orario">
      <span class="ora">${esc(n.testo)}</span>
      ${n.cap ? `<span class="cap">${esc(n.cap)}</span>` : ''}
    </div>
    <div class="dove">
      <div class="destinazione">${etichettaTreno(d)}</div>
      <span class="meta prossima">${f
        ? `<span class="nome">prossima ${esc(f.name)}</span>${
          f.scheduled ? `<span class="quando">· ${esc(f.scheduled)}</span>` : ''}`
        : 'viaggio concluso'}</span>
    </div>
    <div class="binario${cambio ? ' cambiato' : ''}">
      ${f && f.platform
        ? `<span class="num">${esc(f.platform)}</span>
           <span class="cap">${cambio ? esc(cambio) : 'BIN'}</span>`
        : '<span class="ignoto" title="Binario non ancora assegnato">–</span>'}
    </div>
    <div class="adesso">${doveAdesso(d, lettoIl)}</div>`;
}

function schedaSeguito(t) {
  const v = viaggiSeguiti.get(chiaveTreno(t));
  const d = v && v.stato === 'ok' ? v.dati : null;
  const link = rottaTreno(t);

  // Finché la posizione non è arrivata la scheda dice comunque di che treno si
  // tratta: l'etichetta è quella con cui la si è salvata, e senza sarebbe una
  // riga vuota proprio all'apertura dell'app.
  if (!d) {
    const nota = v && v.stato === 'errore'
      ? 'posizione non disponibile adesso' : 'cerco dov\'è il treno…';
    return `<li class="treno"><a class="riga-treno seguito senza-gallone" href="${link}">
      <div class="orario"><span class="ora">–</span></div>
      <div class="dove">
        <div class="destinazione">${etichettaTreno({ category: t.cat, number: t.n, terminus: t.capolinea })}</div>
        <span class="meta">${nota}</span>
      </div>
      <div class="binario"><span class="ignoto">–</span></div>
    </a></li>`;
  }

  const classi = ['treno'];
  if (numeroGrande(d).inRitardo) classi.push('in-ritardo');
  if (d.arrived) classi.push('concluso');
  return `<li class="${classi.join(' ')}">
    <a class="riga-treno seguito senza-gallone" href="${link}">
      ${corpoSeguito(d, v.lettoIl)}
    </a>
  </li>`;
}

/* La scheda di un treno seguito, aperta a tutta pagina: sopra le stesse tre
   cose della riga in home, sotto il viaggio intero con gli orari a cui è
   passato davvero e il binario di ogni fermata. */
function disegnaTreno(t) {
  const k = chiaveTreno(t);
  const v = viaggiSeguiti.get(k);
  const d = v && v.stato === 'ok' ? v.dati : null;
  const salvato = eSeguito(t);
  // Da seguire si prende il viaggio quando c'è, perché porta l'etichetta
  // giusta; altrimenti quello che si era salvato, e in ultimo la sola rotta.
  // La destinazione però viene sempre dal segnalibro: qui non c'è un tabellone
  // da cui leggerla, e ripremere la stella non deve perderla.
  const segnalibro = seguiti().find((x) => chiaveTreno(x) === k);
  const oggetto = d ? daSeguire(d, (segnalibro || t).a) : (segnalibro || t);

  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">${etichettaTreno(d || { category: t.cat, number: t.n, terminus: t.capolinea })}</h1>
      <button class="tasto" type="button" data-segui="${esc(JSON.stringify(oggetto))}"
              aria-pressed="${salvato}"
              aria-label="${salvato ? 'Smetti di seguire questo treno' : 'Segui questo treno'}"
              >${icona('segnalibro', salvato)}</button>
    </div>
    <div class="sottotitolo">${d && d.origin ? `da ${esc(d.origin)} · ` : ''}<span class="vivo">aggiornato <span id="eta">${eta()}</span></span></div>`;

  if (!d) {
    app.innerHTML = v && v.stato === 'errore'
      ? '<p class="errore">Il viaggio di questo treno non è disponibile adesso.</p>'
      : `<ul>${scheletro().repeat(3)}</ul>`;
    return;
  }

  const classi = ['treno'];
  if (numeroGrande(d).inRitardo) classi.push('in-ritardo');
  if (d.arrived) classi.push('concluso');
  app.innerHTML = `
    <div class="${classi.join(' ')}"><div class="riga-treno seguito senza-gallone">${corpoSeguito(d, v.lettoIl)}</div></div>
    ${d.stops && d.stops.length
      ? elencoFermate(d, 'aperta')
      : '<p class="nota">ViaggiaTreno non pubblica le fermate di questo treno.</p>'}
    ${salvato ? '' : `<p class="nota">Non stai seguendo questo treno: tocca il segnalibro
      in alto per tenerlo in cima alla home.</p>`}`;
}

/* Il banner degli avvisi di stazione, in cima alla home e sopra ogni altra
   cosa: un ascensore fuori servizio o una linea deviata per tre mesi cambiano
   il viaggio prima ancora della scelta del treno.

   Chiuso è una striscia sola che scorre, perché gli avvisi di RFI sono lunghi
   quanto un SMS e in una riga non ci starebbero; toccandolo si apre e si legge
   tutto, fermo. È un <details> come le righe delle linee: aperto e chiuso li
   tiene il browser, e non c'è nessuno stato in più da gestire qui.

   Aperto, ogni avviso porta il nome della sua stazione: con due o tre
   preferiti su stazioni diverse, un testo senza etichetta non si sa a chi si
   riferisca — e "ASCENSORI BINARI 14/15 FUORI SERVIZIO" senza sapere in quale
   stazione non è un'informazione. */
function bannerAvvisi() {
  // Si guarda anche che la stazione sia ancora fra i preferiti: togliendone
  // uno la home si ridisegna subito, mentre gli avvisi in mano sono quelli
  // dell'ultima richiesta, e resterebbe un cartello di una stazione che non
  // si segue più.
  const miei = new Set(preferiti().map((p) => p.f));
  const st = (stato.avvisiStazione || [])
    .filter((s) => miei.has(s.placeId) && s.notices && s.notices.length);
  if (!st.length) return '';

  // Lo stesso avviso capita spesso su due stazioni insieme: un cantiere fra due
  // fermate lo pubblicano tutt'e due, con lo stesso testo. Ripeterlo una volta
  // per stazione occuperebbe il doppio dello spazio per dire una cosa sola,
  // quindi si raggruppa sul testo — che la Map tiene nell'ordine in cui è
  // comparso — e le stazioni diventano l'etichetta sopra. Il confronto è sul
  // testo esatto: RFI lo scrive a mano, e due avvisi che dicono la stessa cosa
  // con una parola diversa restano due avvisi, perché non sta a noi decidere
  // che siano lo stesso.
  const perTesto = new Map();
  for (const s of st) {
    for (const t of s.notices) {
      // Un Set e non una lista: se la stessa stazione ripete un avviso, il suo
      // nome non va scritto due volte nella stessa etichetta.
      if (!perTesto.has(t)) perTesto.set(t, new Set());
      perTesto.get(t).add(s.station);
    }
  }

  // Chiuso il nome della stazione non c'è: la striscia scorre e allungarla con
  // un'etichetta per ogni avviso rubarebbe il posto al testo che conta.
  const striscia = [...perTesto.keys()].join('  ·  ');
  const voci = [...perTesto].map(([t, stazioni]) => `<li>
      <span class="stazione-avviso">${esc([...stazioni].join(' · '))}</span>
      <span class="testo-avviso">${esc(t)}</span>
    </li>`).join('');

  return `<details class="avvisi-stazione" data-avvisi${avvisiStazioneAperti ? ' open' : ''}>
    <summary>
      <span class="scorrevole"><span class="scorre">${esc(striscia)}</span></span>
      <span class="etichetta">Avvisi di stazione</span>
      <span class="chevron">${icona('gallone')}</span>
    </summary>
    <ul class="elenco-avvisi-stazione">${voci}</ul>
  </details>`;
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

  return `<section class="sezione">${testa}${barraLinee(stato.linee)}
    ${righe.length ? `<ul class="lista">${righe.map(rigaLinea).join('')}</ul>` : ''}</section>`;
}

/* Il colpo d'occhio sulle 65 linee, che una lista di zero righe non dà: la
   barra dice quanta parte della rete è a posto, e sotto ci sono i numeri
   scritti — la proporzione da sola non si conta, e una fetta rossa larga tre
   pixel va comunque letta. Il minimo di larghezza è per lei: una linea grave
   su sessantacinque è l'unica cosa che questa barra deve far vedere. */
const ETICHETTE_CONTA = {
  regolare: 'regolari',
  critico: 'con criticità',
  grave: 'con gravi criticità',
  ignoto: 'senza stato',
};

function barraLinee(linee) {
  const conta = new Map();
  linee.forEach((l) => {
    const c = statoLinea(l.status).classe;
    conta.set(c, (conta.get(c) || 0) + 1);
  });
  const parti = ['regolare', 'critico', 'grave', 'ignoto'].filter((c) => conta.get(c));
  if (!parti.length) return '';   // il server non ha mandato nessuna linea
  const detto = parti.map((c) => `${conta.get(c)} ${ETICHETTE_CONTA[c]}`).join(', ');
  return `<div class="barra-linee" role="img" aria-label="${esc(detto)}">
      ${parti.map((c) => `<span class="${c}" style="flex:${conta.get(c)}"></span>`).join('')}
    </div>
    <p class="conta-linee" aria-hidden="true">${parti.map((c) =>
      `<span><span class="bollino ${c}"></span>${conta.get(c)} ${ETICHETTE_CONTA[c]}</span>`).join('')}</p>`;
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

  return `<li class="riga con-avvisi">
    <details class="avvisi-linea" data-linea="${esc(l.code)}"${lineeAperte.has(l.code) ? ' open' : ''}>
      <summary class="riga-tocco">
        ${nome}
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
  const gia = avvisiLinea.get(codice);
  // Un tentativo andato male si può rifare chiudendo e riaprendo: uno riuscito
  // no, che è il punto di tenerselo.
  if (gia && gia.stato !== 'errore') return;
  avvisiLinea.set(codice, { stato: 'attesa' });
  try {
    // Senza un tetto, una rete che non risponde lascia "Cerco le
    // comunicazioni…" davanti a chi ha aperto la riga finché non ricarica. Il
    // tabellone rinuncia dopo dieci secondi, quindi quindici qui sono il caso
    // in cui non risponde nemmeno lui.
    const r = await fetch(API.avvisiLinea(codice), { signal: AbortSignal.timeout(15_000) });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `errore ${r.status}`);
    avvisiLinea.set(codice, { stato: 'ok', dati: (await r.json()).notices || [] });
  } catch (e) {
    avvisiLinea.set(codice, {
      stato: 'errore',
      dati: e.name === 'TimeoutError' ? 'Comunicazioni non raggiungibili.' : e.message,
    });
  }
  // Le righe delle linee stanno anche in home, non solo nell'elenco completo:
  // ridisegnando solo l'elenco, chi apriva una riga dalla home restava con
  // "Cerco le comunicazioni…" davanti per sempre.
  aggiornaVista();
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

/* Quando avvisarti: la schermata che decide a che ore le notifiche sulle linee
   possono suonare.

   Ogni fascia porta i suoi giorni, e non ce n'è un elenco solo per tutte:
   andata e ritorno sono due fasce sugli stessi giorni, ma il sabato mattina di
   chi lavora un turno è una terza fascia con giorni suoi, e un elenco unico di
   giorni non saprebbe dirlo. */
function disegnaNotifiche() {
  const elenco = fasce();
  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="${ROTTA_LINEE}" aria-label="Torna alle linee">${icona('indietro')}</a>
      <h1 class="titolo">Quando avvisarti</h1>
    </div>
    <div class="sottotitolo">${elenco.length
      ? 'Le notifiche sulle linee arrivano solo in queste fasce'
      : 'Adesso le notifiche arrivano a qualunque ora'}</div>`;

  app.innerHTML = `
    <p class="nota">${elenco.length
      ? 'Fuori dalle fasce non si perde niente: quello che è ancora in corso quando una fascia si apre arriva in quel momento. Quello che è rientrato prima, no.'
      : 'Aggiungi una fascia per riceverle solo quando ti servono — per esempio andata e ritorno dal lavoro.'}</p>
    ${elenco.length ? `<ul class="fasce">${elenco.map(rigaFascia).join('')}</ul>` : ''}
    <p class="riga-aggiungi">
      <button class="btn-testo" type="button" data-aggiungi-fascia
              ${elenco.length >= MAX_FASCE ? 'disabled' : ''}>+ Aggiungi una fascia</button>
    </p>
    ${elenco.length >= MAX_FASCE ? '<p class="nota">Più di così non serve: sono già una settimana intera.</p>' : ''}
    ${fasceValide() ? '' : '<p class="nota guasta">Una fascia ha due orari uguali: finiscila e le notifiche riprendono a seguirla.</p>'}
    ${campanelle().length ? '' : '<p class="nota">Non hai ancora nessuna campanella accesa: le fasce valgono da quando ne accendi una.</p>'}`;
}

const MAX_FASCE = 8;

function rigaFascia(f, i) {
  const giorni = GIORNI.map((g) => {
    const acceso = (f.giorni || []).includes(g.v);
    return `<button class="giorno${acceso ? ' acceso' : ''}" type="button"
                    data-giorno="${i}:${g.v}" aria-pressed="${acceso}"
                    aria-label="${nomeGiorno(g.v)}">${g.l}</button>`;
  }).join('');
  return `<li class="fascia${fasciaCompleta(f) ? '' : ' incompleta'}">
    <div class="fascia-testa">
      <span class="fascia-quando">${esc(f.da || '--:--')} – ${esc(f.a || '--:--')}</span>
      <button class="btn-testo" type="button" data-togli-fascia="${i}">Rimuovi</button>
    </div>
    <div class="giorni" role="group" aria-label="Giorni della fascia">${giorni}</div>
    ${(f.giorni || []).length ? '' : '<p class="nota-fascia">Nessun giorno scelto: vale tutti i giorni.</p>'}
    <div class="ore">
      <label>dalle <input type="time" data-ora="${i}:da" value="${esc(f.da || '')}"></label>
      <label>alle <input type="time" data-ora="${i}:a" value="${esc(f.a || '')}"></label>
    </div>
  </li>`;
}

const nomeGiorno = (v) =>
  ['domenica', 'lunedì', 'martedì', 'mercoledì', 'giovedì', 'venerdì', 'sabato'][v];

/* Ogni tocco salva e riallinea il server. Non c'è un tasto "salva": una fascia
   scritta e non salvata è una notifica che non arriva senza che nessuno l'abbia
   deciso, e il gesto in più lo si scopre solo quando è troppo tardi. */
function cambiaFasce(muta) {
  const f = fasce();
  muta(f);
  scriviFasce(f);
  disegna();
  sincronizzaNotifiche();
}

function disegnaLinee() {
  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">Stato linee</h1>
      <a class="tasto" href="${ROTTA_NOTIFICHE}"
         aria-label="Quando ricevere le notifiche">${icona('orologio')}</a>
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
  const d = viaggio && viaggio.stato === 'ok' ? viaggio.dati : null;
  if (d && d.stops && d.stops.length) return viaggioReale(d) + bottoneSegui(d);

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
  return `<ol class="fermate">${voci}</ol>${nota}${d ? bottoneSegui(d) : ''}`;
}

/* "Segui" sta qui dentro, nella scheda aperta, e non sulla riga chiusa del
   tabellone.

   Il motivo è che solo qui si sa se c'è qualcosa da seguire: le coordinate con
   cui richiedere il viaggio le ha soltanto ViaggiaTreno, che su un tabellone
   non conosce tutti i treni. Un pulsante su ogni riga sarebbe morto una volta
   su due, e per scoprire quale volta bisognerebbe premerlo. Aprire la scheda,
   che è il gesto con cui si va a vedere dov'è il treno, è anche quello che
   scopre se si può seguire. */
function bottoneSegui(d) {
  if (!d.id || !d.id.origin) return '';
  // Sugli arrivi `stato.a` non è una destinazione — quel tabellone non ne ha
  // una — e passarla vorrebbe dire accendere una fermata a caso.
  const t = daSeguire(d, stato.arrivi ? null : stato.a);
  const gia = eSeguito(t);
  // Le coordinate viaggiano nell'attributo come JSON: sono tre più
  // l'etichetta, e cinque attributi separati sarebbero cinque cose da tenere
  // allineate invece di una.
  return `<p class="segui-riga">
    <button class="btn-testo segui" type="button" data-segui="${esc(JSON.stringify(t))}"
            aria-pressed="${gia}">${icona('segnalibro', gia)}${
      gia ? 'Lo stai seguendo' : 'Segui questo treno'}</button>
  </p>`;
}

/* Le fermate secondo ViaggiaTreno: quelle già servite portano l'ora a cui il
   treno ci è passato davvero, le altre solo quella prevista.

   Non si prova a proiettare il ritardo sulle fermate future: ViaggiaTreno non
   lo fa — lì lascia zero, che è un campo non compilato e non una previsione — e
   inventarlo qui vorrebbe dire stampare un orario che nessuno ha calcolato,
   con l'aria di essere un dato. */
function viaggioReale(d) {
  const dove = d.tracked && d.lastSeen && d.lastSeen.station
    ? `<p class="viaggio-nota">rilevato a ${esc(d.lastSeen.station)}${
        d.lastSeen.time ? ` alle ${esc(d.lastSeen.time)}` : ''}</p>`
    : '<p class="viaggio-nota">non ancora partito</p>';
  return fasciaProvvedimento(d) + dove + elencoFermate(d);
}

function elencoFermate(d, classe) {
  // Il binario sta in colonna, e una colonna vuole una cella su ogni riga
  // anche dove il binario non c'è: se la si salta, l'ora di quella riga slitta
  // nella colonna del binario e la lista torna disallineata proprio dove
  // mancava il dato. La colonna esiste solo se almeno una fermata ne ha uno,
  // altrimenti sarebbe una colonna vuota lungo tutto il viaggio.
  const conBinari = d.stops.some((f) => f.platform);
  const voci = d.stops.map((f) => {
    const classi = [];
    if (f.passed) classi.push('passata');
    if (f.chosen) classi.push('meta-scelta');
    const ora = f.passed && f.actual
      ? `${esc(f.actual)}${f.delay ? ` <small>${f.delay > 0 ? '+' : ''}${f.delay}</small>` : ''}`
      : esc(f.scheduled);
    return `<li class="${classi.join(' ')}">
      <span>${esc(f.name)}</span>${conBinari ? binarioFermata(f) : ''}<time>${ora}</time></li>`;
  }).join('');
  const classi = ['fermate'];
  if (conBinari) classi.push('con-binari');
  if (classe) classi.push(classe);
  return `<ol class="${classi.join(' ')}">${voci}</ol>`;
}

/* Il binario di ogni fermata, che è la seconda cosa che si chiede da sopra un
   treno dopo "quanto ritardo ho".

   Quando è cambiato si scrive il previsto sbarrato accanto a quello nuovo,
   invece di colorare il numero e basta: un numero acceso in mezzo a una lista
   di numeri spenti dice che qualcosa è successo, non cosa, e sono due caratteri
   in più su una riga che ci sta comoda. */
function binarioFermata(f) {
  // Una cella vuota e non niente: è quella che tiene la colonna in piedi dove
  // il binario non c'è ancora, che sulle fermate lontane è la norma.
  if (!f.platform) return '<span class="bin-fermata vuoto"></span>';
  return `<span class="bin-fermata${f.platformScheduled ? ' cambiato' : ''}">${
    f.platformScheduled ? `<s>${esc(f.platformScheduled)}</s> ` : ''}${esc(f.platform)}</span>`;
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

/* Il segnalibro si tocca da due posti — la scheda aperta di un tabellone e la
   scheda del treno stesso — e fa la stessa cosa da tutti e due. */
function alternaSeguitoDa(el) {
  const t = JSON.parse(el.dataset.segui);
  alternaSeguito(t);
  // Appena seguito, il viaggio si scarica subito: tornando in home la scheda
  // dev'essere già piena, non ancora in attesa. Il server lo tiene in cache
  // trenta secondi, quindi è la stessa lettura appena fatta.
  if (eSeguito(t)) caricaViaggioSeguito(t).then(disegna);
  disegna();
}

app.addEventListener('click', (e) => {
  const t = e.target;
  if (t.closest('[data-aggiungi-fascia]')) {
    // La proposta è quella che manca: chi ha già l'andata sta quasi sempre
    // aggiungendo il ritorno.
    cambiaFasce((f) => f.push({ ...FASCE_PROPOSTE[Math.min(f.length, 1)] }));
  }
  else if (t.closest('[data-togli-fascia]')) {
    const i = Number(t.closest('[data-togli-fascia]').dataset.togliFascia);
    cambiaFasce((f) => f.splice(i, 1));
  }
  else if (t.closest('[data-giorno]')) {
    const [i, g] = t.closest('[data-giorno]').dataset.giorno.split(':').map(Number);
    cambiaFasce((f) => {
      const giorni = f[i].giorni || [];
      f[i].giorni = giorni.includes(g) ? giorni.filter((x) => x !== g) : [...giorni, g].sort();
    });
  }
  else if (t.closest('[data-segui]')) alternaSeguitoDa(t.closest('[data-segui]'));
  else if (t.closest('[data-apri]')) apriScelta(t.closest('[data-apri]').dataset.apri);
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
  if (d instanceof HTMLDetailsElement && 'avvisi' in d.dataset) {
    avvisiStazioneAperti = d.open;
    return;
  }
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
  const segui = e.target.closest('[data-segui]');
  if (segui) { alternaSeguitoDa(segui); return; }
  if (!e.target.closest('[data-preferito]')) return;
  alternaPreferito({ f: stato.da, t: stato.arrivi ? null : stato.a, a: stato.arrivi || undefined });
  disegnaRisultati();
});

/* Gli orari arrivano da un <input type="time">, che sul telefono apre la ruota
   di sistema: la modifica si sente su change e non su input, o si salverebbe
   una fascia a ogni scatto della ruota — e con essa una richiesta al server. */
app.addEventListener('change', (e) => {
  const el = e.target.closest('[data-ora]');
  if (!el) return;
  const [i, quale] = el.dataset.ora.split(':');
  cambiaFasce((f) => { f[Number(i)][quale] = el.value; });
});

window.addEventListener('hashchange', cambiaRotta);
// Prima della prima rotta: la home deve poter disegnare le schede seguite con
// l'ultima lettura salvata, senza aspettare una rete che magari non c'è.
idrataViaggi();
cambiaRotta();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('sw.js').catch(() => {});
    // Riallinea l'abbonamento a ogni avvio: il servizio potrebbe averlo perso,
    // e chi ha una campanella accesa non deve accorgersene.
    if (campanelle().length) sincronizzaNotifiche();
  });
}
