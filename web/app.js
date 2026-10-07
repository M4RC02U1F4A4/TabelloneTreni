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
    + (t.a ? `&to=${encodeURIComponent(t.a)}` : '')
    + (t.f ? `&from=${encodeURIComponent(t.f)}` : ''),
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

// Una volta al minuto, come chiesto — ed è quanto ci mette la barretta sotto
// l'intestazione a svuotarsi, quindi cambiarlo qui cambia anche quello che
// quella barretta promette.
const RINFRESCO = 60_000;

/* Oltre quanto una lettura è "vecchia", cioè da dire. Fresca non si dice:
   "letto adesso" sarebbe una riga in più che non informa nessuno, e sotto
   l'intestazione c'è già la barretta a contare. Serve nei casi in cui a
   schermo c'è un dato che non è di adesso — l'app riaperta senza rete, o un
   aggiornamento andato male — e sono proprio quelli in cui tacere farebbe
   passare il vecchio per nuovo. */
const VECCHIA = 2 * 60_000;
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

/* Dove sei, secondo il telefono.

   Non è la posizione del treno presa da ViaggiaTreno: quella arriva come nome
   di un luogo — "1°BIVIO FIDENZA OVEST" — che spesso non è una stazione e non
   esiste in nessun elenco, quindi non si può mettere su una mappa. Il GPS
   risponde invece alla domanda vera, che è quanto manca alla tua fermata.

   La posizione non lascia il telefono: serve a disegnare e a fare una
   sottrazione, e non c'è nessuna ragione per cui il server debba saperla. */
let posizione = null;   // { lat, lon, metri, quando } oppure { errore }
let guardiaGPS = null;  // l'identificativo di watchPosition, per poterlo spegnere

// Chi l'ha già concessa una volta non se lo deve richiedere ogni volta: il
// permesso lo tiene il browser, questa è solo l'intenzione dichiarata.
const vuolePosizione = () => leggi('tt.posizione', false) === true;

/* Se il permesso vero c'è, che è un'altra cosa dall'intenzione.

   Su Android il browser se lo ricorda e non chiede più niente; su iOS il
   permesso dura la sessione, e riaprendo l'app la finestra di sistema torna.
   Chiedendolo da soli all'apertura della scheda, quella finestra compariva
   appena aperta l'app a chi non aveva chiesto niente — che è la ragione per
   cui adesso il GPS parte da solo *solo* dove non costa una domanda.

   Vive quanto la pagina: appena una lettura riesce si sa che il permesso in
   questa sessione c'è, e uscendo dalla scheda e rientrandoci non si fa
   ricominciare da un tocco. Ricaricando riparte da zero, che è giusto: è
   esattamente quello che fa anche il permesso di Safari. */
let gpsConcesso = false;

/* Il permesso secondo il browser, quando il browser lo sa dire.

   Safari non risponde su `geolocation` — a seconda della versione solleva o
   dice `prompt` comunque — e un'eccezione qui vale come "non lo so", che
   porta all'unica scelta prudente: aspettare il tocco. */
async function permessoDato() {
  if (gpsConcesso) return true;
  if (!navigator.permissions || !navigator.permissions.query) return false;
  try {
    const s = await navigator.permissions.query({ name: 'geolocation' });
    return s.state === 'granted';
  } catch {
    return false;
  }
}

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
// Gli avvisi di stazione, aperti o chiusi sotto il loro gettone. Vale come
// lineeAperte: la home si ridisegna una volta al minuto, e senza ricordarselo
// l'elenco si richiuderebbe in faccia a chi stava leggendo l'avviso per intero.
let avvisiStazioneAperti = false;
// Gli scioperi si ricordano se li avevi aperti, come gli avvisi: la home si
// ridisegna spesso e richiuderli sotto le dita sarebbe fastidioso.
let scioperiAperti = false;
// Il prossimo treno di ogni tratta salvata, che la sua tessera in home scrive
// senza doverla aprire. Indicizzato su chiaveTratta.
const prossimi = new Map(); // chiave -> { stato: 'ok'|'errore', treno }
// Cosa cerca il pannello in fondo alla home: una tratta, oppure il tabellone
// intero di una stazione. Vive quanto la pagina: riaprendo l'app si cerca
// quasi sempre una tratta.
let modoRicerca = 'tratta';
// Gli abituali aperti per cambiarne i giorni. Chiusi, sono una riga.
const abitualiAperti = new Set();
// La famiglia di treni a cui si è ristretto il tabellone di una stazione, vuota
// per tutti. Vale per il tabellone che si sta guardando e si azzera cambiandolo.
let filtroTipo = '';
// Le fermate già servite ripiegate sotto "N fermate precedenti", aperte o no,
// per viaggio: la scheda si ridisegna ogni minuto e non deve richiudersi.
const precedentiAperte = new Set();
// I gruppi di linee aperti nell'elenco completo, per lo stesso motivo.
const gruppiLineeAperti = new Set();
let timerRinfresco = null;
let timerEta = null;
let richiestaInCorso = 0;
// L'ultimo ritorno in primo piano, per non rileggere due volte quando il
// telefono manda più di un evento per lo stesso rientro. Vedi alRientro().
// Parte da adesso perché l'apertura della pagina è già una lettura: il `focus`
// che arriva subito dopo non deve rifarla.
let ultimoRientro = Date.now();

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

/* RFI manda i nomi di stazione urlati — "MILANO PORTA GARIBALDI" — e nell'app
   erano l'unica cosa scritta così: le linee di Trenord arrivano già in tondo, e
   fra le due i preferiti sembravano un errore invece che il nome di un posto.
   Qui vengono rimessi in tondo per come si scrivono in italiano.

   La trasformazione non cambia la lunghezza di un carattere, ed è un vincolo
   che serve: evidenzia() taglia il nome vero usando le posizioni trovate sul
   nome normalizzato, e un titolo che allunga o accorcia sposterebbe il <mark>.

   Cosa resta com'era, e perché:
   - PM, PC, PES sono posti di movimento, comunicazione ed esercizio: sigle di
     esercizio ferroviario, non parole. AV è l'alta velocità, MI e NO sono le
     province fra parentesi che distinguono i due CUZZAGO.
   - Le vocali finali accentate arrivano da RFI come apostrofo ("CANICATTI'").
     Restano apostrofo: indovinare fra grave e acuta su duemila nomi vuol dire
     sbagliarne qualcuno, e un accento sbagliato è peggio del segno che c'era.
   - Dopo un punto, i troncamenti di due lettere sono minuscoli ("P.ta", "C.le")
     e tutto il resto no ("S.Fratello", "R.R."): è la differenza fra una parola
     abbreviata e un'iniziale puntata. */
const MINORI = new Set(['a', 'agli', 'ai', 'al', 'all', 'alla', 'alle', 'allo', 'd', 'da', 'dal',
  'dall', 'dalla', 'de', 'degli', 'dei', 'del', 'dell', 'della', 'delle', 'dello', 'di', 'e',
  'gli', 'i', 'il', 'in', 'l', 'la', 'le', 'lo', 'per', 'su', 'sui', 'sul', 'sull', 'sulla',
  'sulle', 'sullo']);
const SIGLE = new Set(['AV', 'MI', 'NO', 'PC', 'PES', 'PM']);

const grande = (p) => p[0].toUpperCase() + p.slice(1);

function titolo(v) {
  const s = String(v).toLowerCase();
  return s.replace(/[a-z\u00e0-\u00ff0-9]+/g, (parola, i) => {
    if (SIGLE.has(parola.toUpperCase())) return parola.toUpperCase();
    if (i === 0) return grande(parola);
    const prima = s[i - 1];
    if (prima === '.' || prima === "'") return parola.length === 2 ? parola : grande(parola);
    return MINORI.has(parola) ? parola : grande(parola);
  });
}

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
  // Il treno abituale, che torna ogni giorno: due frecce in giro. Da piena si
  // riempiono le sole punte — i due archi riempiti diventerebbero due
  // triangoli fra l'arco e la sua corda, cioè una macchia.
  ripeti: '<path d="M17 1l4 4-4 4"/><path fill="none" d="M3 11V9a4 4 0 0 1 4-4h14"/>' +
    '<path d="M7 23l-4-4 4-4"/><path fill="none" d="M21 13v2a4 4 0 0 1-4 4H3"/>',
  orologio: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  mira: '<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="2.5" fill="currentColor"/><path d="M12 2v3"/><path d="M12 19v3"/><path d="M2 12h3"/><path d="M19 12h3"/>',
  // La "i" delle note di RFI e della legenda dei ritardi.
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 8h.01"/><path d="M11 12h1v4h1"/>',
  // Il treno sulla linea delle fermate, fra le due dove si trova.
  treno: '<rect x="6" y="3" width="12" height="14" rx="3"/><path d="M6 11h12"/><path d="M9 21l-1-3"/>' +
    '<path d="M15 21l1-3"/>',
  // Il triangolo dei gettoni di sciopero e di avviso: lo stesso segno per
  // tutti e due, il colore dice quale.
  allerta: '<path d="M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>' +
    '<path d="M12 9v4"/><path d="M12 17h.01"/>',
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

/* Le quattro liste salvate — preferiti, campanelle, treni seguiti, abituali —
   si accendono e spengono allo stesso modo: via se c'è, dentro se non c'è.
   `chiaveDi` dice cosa rende uguali due voci; `inCima` è per le liste in cui
   l'ultima cosa aggiunta va davanti. */
function alterna(chiave, voce, chiaveDi, inCima = false) {
  const k = chiaveDi(voce);
  const elenco = leggi(chiave, []);
  const resto = elenco.filter((x) => chiaveDi(x) !== k);
  if (resto.length === elenco.length) {
    if (inCima) resto.unshift(voce); else resto.push(voce);
  }
  scrivi(chiave, resto);
}

/* Le due stazioni scritte nella ricerca. Stavano solo in memoria, e su un
   telefono che chiude le applicazioni quando gli pare sparivano di continuo:
   si riapriva l'app e il modulo era di nuovo vuoto, con la stazione di partenza
   da ridire ogni volta. Sono l'unica cosa della home che si compila a mano, e
   la sola che non si ricordava. */
const scriviCampi = () => scrivi('tt.campi', { da: stato.da, a: stato.a });

/* Si rileggono una volta sola all'avvio e non a ogni ritorno in home: dentro
   la sessione i campi seguono già quello che si sta guardando, e rimetterceli
   sopra a ogni giro cancellerebbe la stazione appena scelta. */
function idrataCampi() {
  const c = leggi('tt.campi', {});
  stato.da = c.da || null;
  stato.a = c.a || null;
}

const preferiti = () => leggi('tt.preferiti', []);
const chiaveTratta = (p) => `${p.f}>${p.t || ''}${p.a ? '>a' : ''}`;

function alternaPreferito(p) { alterna('tt.preferiti', p, chiaveTratta, true); }
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

// Il nome IANA del fuso del telefono. Se il browser non lo dice il campo resta
// fuori dal JSON, e il server usa l'ora italiana.
const fusoDelTelefono = () => Intl.DateTimeFormat().resolvedOptions().timeZone;
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

const alternaCampanella = (codice) => alterna('tt.campanelle', codice, (c) => c, true);

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

const alternaSeguito = (t) => alterna('tt.seguiti', t, chiaveTreno);

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
  const vivi = new Set(seguitiInHome().map(chiaveTreno));
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
const daSeguire = (d, a, f) => ({
  o: d.id.origin, n: d.id.number, d: d.id.date,
  cat: d.category, capolinea: d.terminus,
  ...(a ? { a } : {}),
  // E il tabellone da cui lo si è seguito. È quello che permette alla scheda di
  // essere la riga di quel tabellone, con il ritardo che RFI pubblica: senza,
  // resta la sola misura di ViaggiaTreno, che su un regionale non ancora
  // partito non esiste — ed era il motivo per cui la scheda diceva "non ancora
  // partito" a chi il treno lo stava perdendo.
  ...(f ? { f } : {}),
});

/* Come un treno seguito si presenta al server, che lo deve poter chiedere a
   ViaggiaTreno per conto suo mentre l'app è chiusa.

   Sono gli stessi quattro campi, con i nomi che usa il server: le tre
   coordinate del viaggio e la stazione da cui si sale, che è dove le notifiche
   devono smettere. `f` può mancare — un treno seguito da una rotta senza
   tabellone sotto — e zero là significa "non lo sappiamo": il server allora
   avvisa fino al capolinea invece di smettere prima. */
const perIlServer = (t) => ({
  origin: t.o, number: String(t.n), date: t.d, from: Number(t.f) || 0,
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

/* I treni abituali: quello che si prende ogni mattina, salvato una volta sola.

   È un segnalibro senza giorno. Al posto della data di partenza porta l'ora a
   cui passa dalla stazione da cui si sale — quella della riga del tabellone,
   in ora italiana come tutti gli orari RFI — e i giorni della settimana in cui
   lo si prende, con la convenzione di time.Weekday delle fasce: è il server
   che ne fa il treno di oggi e manda le notifiche, e due convenzioni sarebbero
   un punto in più in cui sbagliare. Nessun giorno è ammesso: vuol dire mai, ed
   è il modo di metterlo in pausa per le ferie senza perderlo.

   La chiave non ha il giorno, e ha la stazione: lo stesso treno preso da due
   stazioni diverse — l'andata da una, un recupero da un'altra — sono due
   abitudini, con due ore. */
const abituali = () => leggi('tt.abituali', []);
const chiaveAbituale = (x) => `${x.o}|${x.n}|${x.f}`;
const eAbituale = (x) => abituali().some((y) => chiaveAbituale(y) === chiaveAbituale(x));

const alternaAbituale = (x) => alterna('tt.abituali', x, chiaveAbituale);

/* Come un abituale si presenta al server: gli stessi nomi di TrenoAbituale. La
   fermata dove si scende resta qui, perché al server non serve. */
const abitualePerIlServer = (x) => ({
  origin: x.o, number: String(x.n), from: Number(x.f) || 0, at: x.at, days: x.days || [],
});

/* Il treno abituale di oggi: lo stesso segnalibro che si metterebbe a mano,
   con le tre coordinate di ViaggiaTreno, finché serve.

   Esiste da dieci minuti prima dell'ora a tre ore dopo, e solo nei giorni
   scelti: prima non c'è niente da guardare, dopo il treno è arrivato da un
   pezzo. È la stessa finestra che usa il server per generarlo, e la stessa
   chiave — quindi un segnalibro messo a mano sullo stesso treno è una scheda
   sola, non due.

   Tutto in ora di Roma e non in quella del telefono: l'ora salvata è quella
   del tabellone RFI, e il giorno di partenza di ViaggiaTreno è la mezzanotte
   italiana. Un telefono rimasto sul fuso di un viaggio a Londra deve vedere il
   treno delle 7:12 alle 7:12 di Milano, non alle 7:12 di là.

   Si guardano anche ieri e domani, come fa il server: la finestra può
   scavalcare la mezzanotte, e il treno delle 23:30 si segue ancora all'una.

   ponytail: un treno che parte dall'origine prima di mezzanotte e passa dalla
   stazione di salita dopo avrebbe il giorno sbagliato, come sul server. */
const QUADRANTE_ROMA = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Europe/Rome', hourCycle: 'h23',
  year: 'numeric', month: 'numeric', day: 'numeric',
  hour: 'numeric', minute: 'numeric', second: 'numeric',
});

// Quello che segna l'orologio di Roma all'istante `ms`, scritto come se fosse
// UTC: così giorno, ora e giorno della settimana si leggono con i getUTC*.
function quadranteRoma(ms) {
  const p = Object.fromEntries(QUADRANTE_ROMA.formatToParts(ms).map((x) => [x.type, Number(x.value)]));
  return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second);
}

// Il contrario: l'istante in cui l'orologio di Roma segna `q`. Due giri perché
// lo scarto dall'UTC di un'ora può non essere quello di adesso — la notte in
// cui cambia l'ora legale, mezzanotte e le sette hanno scarti diversi.
function istanteRoma(q) {
  const prova = q - (quadranteRoma(q) - q);
  return q - (quadranteRoma(prova) - prova);
}

// I minuti dalla mezzanotte di un orario "HH:MM". NaN se non si legge.
function minutiDi(hhmm) {
  const [h, m] = String(hhmm).split(':').map(Number);
  return h * 60 + m;
}

function abitualiDiOggi(elenco, adesso) {
  const ms = adesso.getTime();
  const qui = new Date(quadranteRoma(ms));
  const oggi = Date.UTC(qui.getUTCFullYear(), qui.getUTCMonth(), qui.getUTCDate());
  const out = [];
  for (const x of elenco) {
    const minuti = minutiDi(x.at || '');
    for (const giorno of [oggi - 86_400_000, oggi, oggi + 86_400_000]) {
      if (!(x.days || []).includes(new Date(giorno).getUTCDay())) continue;
      const parte = istanteRoma(giorno + minuti * 60_000);
      if (ms < parte - 10 * 60_000 || ms >= parte + 3 * 60 * 60_000) continue;
      out.push({
        o: x.o, n: x.n, d: istanteRoma(giorno), f: x.f, cat: x.cat, capolinea: x.capolinea,
        ...(x.a ? { a: x.a } : {}),
      });
      break;
    }
  }
  return out;
}

/* I treni che la home segue: i segnalibri salvati più gli abituali di oggi.

   Gli abituali non si scrivono in `tt.seguiti`: si ricalcolano a ogni giro, e
   scritti resterebbero lì fino alle trentasei ore della pulizia anche dopo
   aver tolto l'abitudine. Per lo stesso motivo non vanno al server come treni
   — se li genera da sé, e mandati due volte sarebbero due notifiche.

   Se c'è anche il segnalibro vince quello, che può portare la fermata dove si
   scende. Un abituale arrivato si toglie come si toglie un segnalibro, dalla
   lettura che lo dice: altrimenti resterebbe in lista per tre ore a dire che è
   arrivato. */
function seguitiInHome() {
  const salvati = seguiti();
  const chiavi = new Set(salvati.map(chiaveTreno));
  return [...salvati, ...abitualiDiOggi(abituali(), new Date()).filter((t) => {
    const v = viaggiSeguiti.get(chiaveTreno(t));
    return !chiavi.has(chiaveTreno(t)) && !(v && v.dati && v.dati.arrived);
  })];
}

function ricorda(id) {
  const r = leggi('tt.recenti', []).filter((x) => x !== id);
  r.unshift(id);
  scrivi('tt.recenti', r.slice(0, 8));
}

/* ------------------------------------------------------------------- dati */

/* Una lettura JSON dal server: la risposta, o null se la pagina si sta
   ricaricando per una versione nuova. Un errore HTTP diventa un'eccezione con
   il messaggio del server, quando c'è, e con lo status, per chi deve
   distinguere un 4xx da un 5xx. */
async function leggiJSON(url, opzioni) {
  const r = await fetch(url, opzioni);
  if (controllaVersione(r)) return null;
  if (!r.ok) {
    const e = new Error((await r.json().catch(() => ({}))).error || `errore ${r.status}`);
    e.status = r.status;
    throw e;
  }
  return r.json();
}

async function caricaStazioni() {
  if (stato.stazioni.length) return;
  const r = await fetch(API.stazioni);
  if (!r.ok) throw new Error('elenco stazioni non disponibile');
  const d = await r.json();
  // In tondo qui e non a ogni disegno: da qui in poi l'elenco, la ricerca, i
  // preferiti e i campi leggono tutti la stessa forma.
  stato.stazioni = d.stations.map(([id, n]) => [id, titolo(n)]);
  stato.canoni = stato.stazioni.map(([, n]) => canon(n));
  stato.nomi = new Map(stato.stazioni);
  // Un id salvato può non esistere più dopo un aggiornamento del catalogo:
  // senza questo il campo resterebbe su "stazione 1393" senza modo di capire
  // cosa sia. Si controlla qui perché è l'unico punto in cui i nomi esistono.
  if (stato.da && !stato.nomi.has(stato.da)) stato.da = null;
  if (stato.a && !stato.nomi.has(stato.a)) stato.a = null;
}

/* Quale tabellone si sta guardando. Serve a una richiesta partita da qui per
   sapere, quando torna, se è ancora quello di prima. */
const chiaveTabellone = () => `${stato.da}|${stato.a}|${stato.arrivi}`;

async function caricaTabellone() {
  const mio = ++richiestaInCorso;
  disegna();
  try {
    // Un limite serve: appesa, la lettura terrebbe il tabellone sugli
    // scheletri e il gesto di aggiornare a girare finché il browser non si
    // arrende, cioè per minuti. Venticinque secondi e non i quindici delle
    // altre letture: il server aspetta RFI fino a venti.
    const d = await leggiJSON(API.tabellone(stato.da, stato.a, stato.arrivi), { signal: AbortSignal.timeout(25_000) });
    if (!d) return;                                // la pagina si sta ricaricando
    if (mio !== richiestaInCorso) return;          // una richiesta più nuova ha già vinto
    stato.dati = d;
    stato.scaricatoIl = Date.now();
    stato.errore = null;
    rileggiSchedeAperte();
  } catch (e) {
    if (mio !== richiestaInCorso) return;
    // Il messaggio del timeout è quello del browser, in inglese.
    stato.errore = e.name === 'TimeoutError' ? 'Tabellone non raggiungibile.' : e.message;
  } finally {
    if (mio === richiestaInCorso) disegna();
  }
}

/* Rilegge il viaggio delle schede che qualcuno ha aperto, insieme al tabellone.

   Solo dei treni che il tabellone appena arrivato porta ancora: `aperti` tiene
   anche quelli partiti mezz'ora fa, e senza il confronto le loro richieste
   continuerebbero a partire ogni minuto per una scheda che non c'è più. */
function rileggiSchedeAperte() {
  if (!aperti.size) return;
  const presenti = new Set((stato.dati.trains || []).map((t) => t.number));
  for (const numero of aperti) {
    if (presenti.has(numero)) scaricaViaggio(numero);
  }
}

/* Le linee non bloccano mai il disegno di quello che le sta intorno: la home
   compare subito e i bollini ci si appoggiano quando arrivano. Se il servizio
   non risponde si tiene da parte il motivo e si va avanti. */
async function caricaLinee() {
  try {
    const d = await leggiJSON(API.linee, { signal: AbortSignal.timeout(15_000) });
    if (!d) return;
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
    stato.lineeErrore = e.name === 'TimeoutError' ? 'Stato delle linee non raggiungibile.' : e.message;
  }
}

/* Il viaggio di un treno seguito.

   Un tentativo andato bene non si butta per uno andato male: la rete di un
   treno in corsa cade a tratti, e l'ultima posizione nota è più utile di una
   scheda che si svuota ogni volta che si entra in galleria.

   Torna vero solo se la lettura è arrivata davvero, ed è quello che decide se
   l'orologio del dato può ripartire. Senza, un giro andato a vuoto rimetteva la
   barretta piena e faceva passare la posizione di prima per quella di adesso:
   la pagina sembrava aggiornarsi proprio nei momenti in cui non lo faceva. */
async function caricaViaggioSeguito(t, forza) {
  const k = chiaveTreno(t);
  const gia = viaggiSeguiti.get(k);
  if (!forza && gia && gia.stato !== 'errore') return false;
  if (!gia) viaggiSeguiti.set(k, { stato: 'attesa' });
  // La destinazione la sa solo il segnalibro: nella rotta `#/t/o/n/d` ci sono
  // le tre coordinate e basta, e un viaggio chiesto da lì tornava senza la
  // fermata accesa. Si risolve qui, che è l'unico punto per cui passano tutte
  // le letture — dalla home e dalla scheda aperta.
  const salvato = seguitiInHome().find((x) => chiaveTreno(x) === k);
  const chiesto = t.a && t.f
    ? t
    : { ...t, a: t.a || (salvato && salvato.a), f: t.f || (salvato && salvato.f) };
  try {
    // Più del tabellone (25 s): il server aspetta ViaggiaTreno fino a venti
    // secondi e le righe di RFI fino a sei, e una scheda che aspetta un po' di
    // più vale più di una che resta vecchia.
    const d = await leggiJSON(API.viaggio(chiesto), { signal: AbortSignal.timeout(30_000) });
    if (!d) return false;
    // Il treno di oggi che era tracciato e adesso non lo è più: ViaggiaTreno
    // l'ha perso per un giro, non è tornato a "non ancora partito". Si tiene
    // l'ultima posizione, come per una lettura fallita, e si torna falso: il
    // dato in mano è quello di prima, e l'orologio non deve ripartire. Il
    // treno di ieri invece prosegue, e il "non tracciato" lo toglie qui sotto.
    if (!d.tracked && !diIeri(t) && gia && gia.stato === 'ok' && gia.dati && gia.dati.tracked) return false;
    viaggiSeguiti.set(k, { stato: 'ok', dati: d, lettoIl: Date.now() });
    ricordaViaggio(k, d);
    // Arrivato, il segnalibro si toglie subito — ma il viaggio resta in mano
    // alla pagina: chi in quel momento lo sta guardando vede che il treno è
    // arrivato, che è la cosa per cui lo seguiva. È la stessa separazione di
    // sempre, fra l'elenco salvato e la copia che la pagina ha già in mano; a
    // sparire è solo la mezz'ora di attesa, che teneva in lista un viaggio
    // finito per nessuno.
    if (d.arrived) smettiDiSeguire(k);
    // Il treno di ieri che ViaggiaTreno non traccia più è finito come quello
    // che il server non conosce: se ne va. Quello di oggi non tracciato resta,
    // è solo non ancora partito; il notturno di ieri ancora in viaggio torna
    // tracciato e resta anche lui.
    else if (!d.tracked && diIeri(t)) smettiDiSeguire(k);
    return true;
  } catch (e) {
    // Un 4xx su un treno di un giorno passato vuol dire che il server non lo
    // accetta più (il 400 dei giorni ammessi): è finito, e il segnalibro se
    // ne va come se fosse arrivato. Un 5xx no: il treno dimenticato da
    // ViaggiaTreno torna come "non tracciato" più sopra, e un 5xx dice solo
    // che ViaggiaTreno non risponde — un timeout, un deploy. Il treno delle
    // 23:30 che arriva alle 00:40 non deve sparire al primo singhiozzo; se
    // non risponde mai più lo toglie potaSeguiti, a 36 ore. Per lo stesso
    // motivo conta solo lo status di una risposta: la rete che cade non ne ha,
    // e non dice niente del treno.
    if (e.status < 500 && diIeri(t)) smettiDiSeguire(k);
    // L'ultima lettura buona resta, in memoria e su disco: su un treno la rete
    // cade a tratti, e la posizione di un minuto fa vale più di una riga vuota.
    // Vale anche per il treno di ieri appena tolto: senza lo stato di errore,
    // in 'attesa' la sua scheda aperta resterebbe a cercarlo per sempre.
    if (!gia || gia.stato !== 'ok') viaggiSeguiti.set(k, { stato: 'errore' });
    return false;
  }
}

/* Se il giorno di partenza è prima della mezzanotte di Roma di adesso.

   Serve a una cosa sola: decidere cosa vuol dire un 4xx del server, o un
   treno non tracciato. Per il treno di oggi è un treno non ancora partito, e
   si tiene; per il treno di ieri è ViaggiaTreno che se l'è dimenticato —
   risponde 204, il server "non tracciato" — e tenerlo voleva dire una scheda
   in home fino a mezzogiorno per un treno arrivato la sera prima. Un 5xx non
   passa di qui: è ViaggiaTreno che non risponde, di ieri o di oggi.

   Un `d` fuori dall'intervallo delle date (una rotta scritta a mano) non è di
   ieri: 8.64e15 è il limite di Date, oltre il quale Intl lancia. */
const diIeri = (t, adesso = Date.now()) => Math.abs(t.d) <= 8.64e15
  && quadranteRoma(t.d) < quadranteRoma(adesso) - (quadranteRoma(adesso) % 86_400_000);

/* Tutti i treni seguiti insieme. Sono pochi per definizione — si seguono i
   treni che si prendono — e partono in parallelo: in fila la home aspetterebbe
   la somma di altrettante letture su un servizio lento. */
async function aggiornaSeguiti(forza) {
  potaSeguiti();
  const elenco = seguitiInHome();
  if (!elenco.length) return;
  const esiti = await Promise.all(elenco.map((t) => caricaViaggioSeguito(t, forza)));
  // Basta un treno letto perché il giro sia servito a qualcosa; se non ne è
  // arrivato nessuno l'ora dell'ultima lettura resta quella vera, e la barretta
  // resta a fondo corsa invece di ripartire su un dato che non è cambiato.
  if (esiti.some(Boolean)) stato.scaricatoIl = Date.now();
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
    const d = await leggiJSON(API.avvisiStazione(ids), { signal: AbortSignal.timeout(15_000) });
    if (!d) return;
    stato.avvisiStazione = d.stations || [];
  } catch {
    stato.avvisiStazione = [];
  }
}

/* Il prossimo treno di ogni tratta salvata, per le tessere in home.

   Una lettura per tratta, in parallelo: le tratte salvate sono poche, e il
   server tiene ogni tabellone trenta secondi sotto un lock per stazione —
   aprire la tratta subito dopo è la stessa lettura, e dieci persone con la
   stessa tratta sono comunque una richiesta sola a RFI. Una lettura andata
   male tiene il treno di prima, come fanno i treni seguiti: un orario di un
   minuto fa vale più di una tessera vuota. */
async function caricaProssimi() {
  await Promise.all(preferiti().map(async (p) => {
    const k = chiaveTratta(p);
    try {
      // Lo stesso limite del tabellone, che il server legge da RFI fino a
      // venti secondi: una richiesta appesa terrebbe la tessera sui tre
      // puntini fino al giro dopo, e il gesto di aggiornare a girare a vuoto
      // aspettandola.
      const d = await leggiJSON(API.tabellone(p.f, p.t, p.a), { signal: AbortSignal.timeout(25_000) });
      if (!d) return;
      prossimi.set(k, { stato: 'ok', treno: (d.trains || [])[0] || null });
    } catch {
      if (!prossimi.has(k)) prossimi.set(k, { stato: 'errore' });
    }
  }));
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

// La richiesta del permesso, se il gesto la merita e non è ancora stata fatta:
// la promessa da passare a sincronizzaNotifiche, o null.
const permessoSe = (c) => (c && statoNotifiche() === 'da-chiedere' ? Notification.requestPermission() : null);

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

/* Manda al server l'elenco aggiornato delle linee, dei treni seguiti e di
   quelli abituali. Vuoti tutti e tre, l'abbonamento si cancella: è lo stesso
   gesto visto dall'altra parte.

   `permesso` è la promessa di Notification.requestPermission(), che il gestore
   del tocco ha già lanciato: su iOS quella chiamata vale solo dentro il gesto,
   quindi si fa lì e si aspetta qui. */
async function sincronizzaNotifiche(permesso) {
  if (permesso) { try { await permesso; } catch { /* prompt chiuso */ } }
  if (statoNotifiche() !== 'concesso') { aggiornaVista(); return; }

  const linee = campanelle();
  // I treni si mandano con le tre coordinate di ViaggiaTreno più la stazione
  // da cui si sale, che è dove il server deve smettere di avvisare. Sono i
  // campi che `daSeguire` salva già: qui si rinominano e basta.
  //
  // Si manda anche un treno su cui si è già saliti — il segnalibro resta fino
  // a `ended` o alle 36 ore, il server lo toglie prima. Non è un problema: al
  // primo giro lo trova già passato dalla fermata di salita, tace e lo
  // ributta fuori. Una lettura, condivisa con chiunque altro segua quel treno.
  const treni = seguiti().map(perIlServer);
  // Gli abituali vanno come sono, senza giorno: il treno di oggi lo fa il
  // server, che è acceso anche quando l'app no.
  const abitudini = abituali().map(abitualePerIlServer);
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
      // Senza campanelle accese e senza treni da aspettare non c'è niente da
      // registrare, e non è il caso di prendersi un abbonamento per poi
      // cancellarlo subito.
      if (!linee.length && !treni.length && !abitudini.length) { notificheErrore = null; aggiornaVista(); return; }
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
        trains: treni,
        habitual: abitudini,
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
};

function apriScelta(quale) {
  campoInModifica = quale;
  campoCerca.value = '';
  // Sul tabellone intero il campo è la stazione e basta: "di partenza" su
  // quella di cui si vogliono gli arrivi direbbe il contrario.
  campoCerca.placeholder = (modoRicerca === 'tratta' && INVITI[quale]) || 'Cerca stazione';
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
  if (campoInModifica === 'da') stato.da = id; else stato.a = id;
  scriviCampi();
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
  location.hash = rottaDi(stato.da, modoRicerca === 'tratta' ? stato.a : null, modoRicerca === 'arrivi');
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
  // Il GPS vive quanto la scheda di un treno: è l'unica vista che lo usa, e
  // tenerlo accesa altrove sarebbe batteria spesa per niente.
  if (r.vista !== 'treno') { fermaPosizione(); posizione = null; mappa = null; mappaAperta = false; }
  else if (vuolePosizione()) avviaSePermesso();

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
    // Le comunicazioni invecchiano mentre le si legge: il servizio rilegge
    // Trenord ogni cinque minuti, e questa è la vista in cui si sta fermi ad
    // aspettare che cambi qualcosa. Senza timer restava quello che c'era
    // quando si è entrati. L'ETag rende quasi gratis i giri in cui il servizio
    // non ha ancora riletto niente.
    avviaTimer(() => caricaLinee().then(() => {
      if (leggiRotta().vista === 'linee') disegna();
    }));
    return;
  }

  if (r.vista === 'treno') {
    stato.errore = null;
    disegna();
    const letto = await caricaViaggioSeguito(r.treno, true);
    if (leggiRotta().vista !== 'treno') return;
    if (letto) stato.scaricatoIl = Date.now();
    disegna();
    avviaTimer(rilettura(r.treno));
    return;
  }

  if (r.vista === 'home') {
    stato.dati = null;
    stato.errore = null;
    stato.arrivi = false;
    // L'orario dell'ultima lettura è lo stesso campo che usa il tabellone:
    // senza azzerarlo, la barretta della home partirebbe dal minuto del
    // tabellone appena lasciato. Torna a esistere quando i treni seguiti sono
    // stati letti davvero.
    stato.scaricatoIl = 0;
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
    // La home si rilegge al minuto come un tabellone. Prima il timer partiva
    // solo con un treno seguito, e senza restava tutto fermo: il semaforo delle
    // linee e gli avvisi di stazione erano quelli di quando si era entrati,
    // mentre la pagina delle linee, a un tocco di distanza, era al minuto.
    avviaTimer(rinfrescaHome);
    rinfrescaHome();
    // La chiave per le notifiche non invecchia: si chiede una volta e basta.
    chiaveNotifiche().then(() => { if (leggiRotta().vista === 'home') disegna(); })
      .catch(() => {});
    return;
  }

  if (r.da !== stato.da) filtroTipo = '';
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

/* Quello che in home invecchia: i treni seguiti, il prossimo treno delle tratte
   salvate, i bollini delle linee e gli avvisi delle stazioni preferite. Lo
   rilegge il timer al minuto e il rientro sull'app, che sono lo stesso bisogno.

   I gruppi restano separati: i bollini arrivano da un secondo servizio e non
   devono far aspettare le schede dei treni, che sono la parte della home che
   si guarda di corsa, né le tessere delle tratte, che sono la seconda. */
function rinfrescaHome() {
  const ridisegna = () => { if (leggiRotta().vista === 'home') disegna(); };
  return Promise.all([
    aggiornaSeguiti(true).then(ridisegna),
    caricaProssimi().then(ridisegna),
    // Il server tiene gli avvisi dieci minuti e le linee hanno l'ETag: un giro al
    // minuto costa due 304 e non due pagine di RFI.
    Promise.all([caricaLinee(), caricaAvvisiStazione()]).then(ridisegna),
  ]);
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

/* Il rientro sull'app: quello che il timer avrebbe fatto mentre la pagina era
   in secondo piano, e che non ha fatto perché lì il timer sta fermo. */
function alRientro() {
  if (document.visibilityState !== 'visible') return;
  // Fra i tre eventi qui sotto, tornare sull'app ne accende spesso due: senza
  // questa soglia la stessa lettura partirebbe in doppia copia a ogni rientro.
  if (Date.now() - ultimoRientro < 2_000) return;
  ultimoRientro = Date.now();
  const vista = leggiRotta().vista;

  // In home il giro è lo stesso del timer: treni seguiti, bollini e avvisi.
  if (vista === 'home') { rinfrescaHome(); return; }

  // I bollini non hanno un timer che gira: senza questo, riaprendo l'app si
  // vedrebbe lo stato di quando la si è chiusa, che per un semaforo è peggio
  // che non vederlo. L'ETag rende la richiesta quasi gratis quando non è
  // cambiato niente.
  if (vista === 'linee') {
    caricaLinee().then(() => { if (leggiRotta().vista === 'linee') disegna(); });
    return;
  }

  if (vista === 'treno') {
    rilettura(leggiRotta().treno)();
    return;
  }

  if (vista !== 'risultati') return;
  // Tornando sull'app dopo un po', il tabellone è vecchio: si aggiorna subito
  // invece di aspettare il prossimo giro.
  if (Date.now() - stato.scaricatoIl > RINFRESCO / 2) caricaTabellone();
  else aggiornaEta();
}

/* Tre eventi per la stessa cosa, perché nessuno dei tre arriva sempre. Su iOS
   l'app installata torna in primo piano a volte senza `visibilitychange`, e
   quando il sistema l'aveva congelata torna con `pageshow`: con il solo primo
   evento si riapriva l'app e si vedeva, ferma, la schermata di ore prima. Una
   lettura in più non si vede, una mancata sì. */
document.addEventListener('visibilitychange', alRientro);
for (const evento of ['pageshow', 'focus']) window.addEventListener(evento, alRientro);

/* Quanto è vecchio il dato, e quanto manca alla prossima lettura.

   La cadenza era scritta — "· ogni minuto" in fondo alla riga — ed era brutta:
   tre frammenti incollati da due puntini, con l'ultimo che penzolava senza
   grammatica. La dice barraCiclo(), che si svuota nell'arco del minuto e si
   riempie a ogni lettura: si guarda una volta e si è capito il ritmo, senza
   una parola in più su una riga che ne ha già abbastanza.

   Per forzare la rilettura c'è il gesto di tirare la pagina in giù, come in
   ogni app, e nessun tasto: un tasto sempre in vista invita a premerlo ogni
   due secondi, e RFI pubblica lo stesso dato per un minuto intero — il server
   lo tiene trenta secondi, quindi un gesto ripetuto costa poco anche lì.

   Il ritardo negativo è quello che tiene la barretta onesta. L'animazione
   riparte da capo a ogni disegno, e disegna() la chiama una dozzina di gestori
   di tocco — aprire le fermate di un treno rimetterebbe la barretta piena
   mentendo sul tempo passato. Ancorandola a scaricatoIl, l'animazione parte già
   a metà strada e dice sempre la verità, comunque la pagina sia stata
   ridisegnata. */
function rigaFreschezza() {
  // "aggiornamento…" non si scrive più. Su un tabellone di partenze fresco il
  // sottotitolo è vuoto, quindi quella parola comparirebbe e sparirebbe ogni
  // minuto portandosi dietro una riga: il contenuto sotto ballerebbe da solo a
  // ogni giro. Che stia caricando lo dicono già gli scheletri la prima volta, e
  // dalla seconda in poi non c'è niente da dire — i treni di prima restano lì
  // finché non arrivano quelli nuovi.
  if (!stato.scaricatoIl) return '';
  // Fresco non si dice. La barretta conta già quel minuto, e "aggiornato 30
  // secondi fa" scritto accanto è lo stesso minuto letto dall'altro verso: due
  // volte la stessa cosa, su una riga che ne ha già abbastanza.
  //
  // Si parla solo quando il dato è rimasto indietro — l'app tenuta in secondo
  // piano, o un giro andato a vuoto senza rete — che è il caso in cui tacere
  // farebbe passare il vecchio per nuovo. È la stessa regola di etaLettura()
  // sulle schede dei treni seguiti, e la stessa soglia.
  if (Date.now() - stato.scaricatoIl < VECCHIA) return '';
  return `letto <span id="eta">${eta()}</span>`;
}

/* Le parti del sottotitolo, senza i puntini orfani di quelle che non ci sono.
   Ne manca sempre almeno una: la freschezza tace quando il dato è fresco, e
   l'origine c'è solo sui treni che ne hanno una. */
const sottotitolo = (...parti) => {
  const dette = parti.filter(Boolean);
  return dette.length ? `<div class="sottotitolo">${dette.join(' · ')}</div>` : '';
};

/* La barretta, che sta sul filo sotto l'intestazione invece che dentro la riga
   di testo: a tutta larghezza il minuto si legge di sfuggita, senza cercarlo.
   Prende il posto del bordo, quindi non costa una riga di layout.

   Va in fondo all'intestazione perché lì il posizionamento assoluto si aggancia
   a .testa, che è sticky e quindi fa da riferimento. */
function barraCiclo() {
  // Durante una rilettura la barretta resta, a fondo corsa: toglierla la
  // farebbe lampeggiare via e tornare a ogni minuto, che è esattamente il
  // movimento che una barra smorzata serve a non fare.
  if (!stato.scaricatoIl) return '';
  const trascorso = Math.min(Date.now() - stato.scaricatoIl, RINFRESCO);
  return `<div class="ciclo" style="--trascorso:-${trascorso}ms" aria-hidden="true"><i></i></div>`;
}

/* La rilettura di un treno seguito, che il timer rifà una volta al minuto. */
const rilettura = (treno) => () => caricaViaggioSeguito(treno, true).then((letto) => {
  if (leggiRotta().vista !== 'treno') return;
  if (letto) stato.scaricatoIl = Date.now();
  disegna();
});

/* Quanto tempo fa, a parole: "un minuto fa", "7 minuti fa". Lo dicono il
   tabellone, la lettura di un treno seguito e la posizione, ognuno con la sua
   soglia sotto la quale tace — quella resta a chi chiama. */
const fa = (ms) => {
  const m = Math.round(ms / 60_000);
  return m === 1 ? 'un minuto fa' : `${m} minuti fa`;
};

function eta() {
  if (!stato.scaricatoIl) return '';
  const s = Math.round((Date.now() - stato.scaricatoIl) / 1000);
  if (s < 10) return 'adesso';
  if (s < 60) return `${s} secondi fa`;
  return fa(s * 1000);
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
  /* La barretta del minuto anche qui, e solo quando c'è un treno seguito.
     Tutta la home si rilegge al minuto, bollini e avvisi compresi, ma la
     barretta conta l'età di un dato e l'unico dato datato qui sono le schede
     seguite: senza, conterebbe un giro di cui non si vede niente.

     Il sottotitolo non c'è più: diceva cosa fa l'app a chi la usa ogni
     mattina, e occupava la riga sopra il treno da prendere. */
  testa.innerHTML = `
    <div class="testa-riga"><h1 class="titolo">Tabellone Treni</h1></div>
    ${seguitiInHome().length ? barraCiclo() : ''}`;

  if (!preferiti().length) modificaPreferiti = false;

  /* Sopra si guarda, sotto si tocca. In cima lo stato del mondo in una fila di
     gettoni, poi il treno che si sta prendendo, poi le tratte salvate col loro
     prossimo treno — tre cose che si leggono senza toccare niente. La ricerca
     sta in un pannello fermo in fondo allo schermo, dove arriva il pollice
     della mano che tiene il telefono, e il resto le scorre sotto.

     Prima la ricerca stava in mezzo alla pagina, fra i preferiti e i bollini:
     si toccava allungando il pollice, e si allontanava a ogni preferito in
     più. In fondo è sempre allo stesso posto, quanti che siano. */
  app.innerHTML = `<div class="home">${statoRete()}${sezioneSeguiti()}${
    sezionePreferiti()}${sezioneAbituali()}</div>${foglioRicerca()}`;
}

/* Le tratte salvate, due per riga: ognuna è una tessera grande quanto un
   pollice, e dice il prossimo treno senza doverla aprire. È la domanda per cui
   si apre l'app la mattina — "quando passa il prossimo" — e prima la risposta
   stava a un tocco e un caricamento di distanza. */
function sezionePreferiti() {
  const fav = preferiti();
  if (!fav.length) return '';
  return `<section class="sezione">
    <div class="testa-sezione">
      <h2 class="etichetta-sezione">Preferiti</h2>
      <button class="btn-testo piccolo" type="button" data-modifica>
        ${modificaPreferiti ? 'Fine' : 'Modifica'}</button>
    </div>
    <ul class="tessere">${fav.map(tesseraPreferito).join('')}</ul>
  </section>`;
}

function tesseraPreferito(p) {
  // Sopra in piccolo da dove, sotto in grande dove: il nome che distingue una
  // tessera dall'altra è la destinazione, perché l'andata e il ritorno partono
  // da due stazioni diverse ma si riconoscono da dove portano.
  const [sopra, nome] = p.a ? ['arrivi a', nomeStazione(p.f)]
    : p.t ? [`da ${nomeStazione(p.f)}`, nomeStazione(p.t)]
      : ['partenze da', nomeStazione(p.f)];
  const corpo = `<span class="sopra">${esc(sopra)}</span>
    <span class="nome">${esc(nome)}</span>
    ${prossimoTreno(p)}`;
  // In modifica la tessera non porta da nessuna parte: un tocco che apre il
  // tabellone accanto alla ✕ che lo toglie sarebbe un errore a un dito di
  // distanza, la stessa ragione per cui la ✕ fuori da "Modifica" non c'è.
  if (modificaPreferiti) {
    return `<li><div class="tessera in-modifica">${corpo}
      <button class="togli" type="button" data-togli="${esc(chiaveTratta(p))}"
              aria-label="Togli dai preferiti">✕</button></div></li>`;
  }
  return `<li><a class="tessera" href="${rottaDi(p.f, p.t, p.a)}">${corpo}</a></li>`;
}

/* Il prossimo treno della tratta: l'ora, il ritardo se c'è, il binario se è
   già assegnato. Il ritardo è uno solo, quello che conta — la misura sul treno
   quando c'è — perché nella tessera due pastiglie non ci stanno; le due fonti
   affiancate restano un tocco più in là, sul tabellone. */
function prossimoTreno(p) {
  const v = prossimi.get(chiaveTratta(p));
  if (!v) return '<span class="poi attesa">…</span>';
  if (v.stato === 'errore') return '<span class="poi attesa">orario non disponibile</span>';
  const t = v.treno;
  if (!t) return '<span class="poi attesa">nessun treno in tabellone</span>';
  if (t.cancelled) {
    return `<span class="poi"><b class="barrato">${esc(t.time)}</b><span class="rit">soppresso</span></span>`;
  }
  const r = ritardoVero(t);
  return `<span class="poi"><b>${esc(t.time)}</b>${r > 0 ? `<span class="rit">+${r}</span>` : ''}${
    t.platform ? `<span${t.platformChanged ? ' class="cambiato"' : ''}>bin ${numeroBinario(t.platform)}</span>` : ''}</span>`;
}

/* I treni seguiti stanno sopra a tutto il resto, ed è l'unica sezione che si
   muove da sola.

   Sopra le tratte salvate perché non sono la stessa cosa: una tratta è un
   posto dove si va spesso, un treno seguito è quello su cui si è adesso, e fra
   le due chi apre l'app di corsa cerca la seconda. Nei giorni in cui non se ne
   segue nessuno la sezione non c'è, e la home è quella di prima. */
function sezioneSeguiti() {
  const elenco = seguitiInHome();
  if (!elenco.length) return '';
  // Il titolo resta per chi ascolta la pagina: a vista la scheda grande in
  // cima si spiega da sé, e "Treni seguiti" sopra era una riga in meno di treno.
  return `<section class="sezione">
    <h2 class="etichetta-sezione solo-lettori">Treni seguiti</h2>
    <ul class="schede">${elenco.sort(perOraDiSalita).map(schedaSeguito).join('')}</ul>
  </section>`;
}

/* L'ora in cui si sale, cioè quella della fermata da cui il treno è stato
   seguito e non quella del capolinea da cui viene: è la stessa che la scheda
   scrive grande, e il solo momento che chi segue quel treno ha in testa. */
const oraDiSalita = (t) => {
  const v = viaggiSeguiti.get(chiaveTreno(t));
  // Finché il viaggio non è arrivato l'ora non si sa: quelle schede vanno in
  // fondo, dove si muoveranno di poco quando la lettura le daterà.
  return (v && v.stato === 'ok' && rigaSeguita(v.dati).time) || '99:99';
};

/* In ordine di partenza, non in ordine di salvataggio: si seguono i treni di
   una giornata, e in cima va quello che si prende prima.

   Il giorno viene per primo perché l'ora è un "HH:MM" senza data: seguendo
   l'ultimo treno di stasera e il primo di domattina, le sole cifre metterebbero
   domattina davanti. */
const perOraDiSalita = (x, y) => x.d - y.d || oraDiSalita(x).localeCompare(oraDiSalita(y));

const etichettaTreno = (d) => {
  const etichetta = esc([d.category, d.number].filter(Boolean).join(' ')) || 'treno';
  return d.terminus ? `${etichetta} <span class="freccia">→</span> ${esc(titolo(d.terminus))}` : etichetta;
};

/* La prima fermata non ancora servita: è dove il treno sta andando adesso.
   Si guarda l'orario reale delle fermate e non l'ultimo rilevamento, che può
   benissimo essere un posto in cui il treno non ferma. */
const prossimaFermata = (d) => (d.stops || []).find((f) => !f.passed) || null;

/* Accende e spegne la lettura della posizione.

   `watchPosition` e non `getCurrentPosition`: su un treno in movimento una
   lettura sola invecchia in un minuto. Ma per lo stesso motivo va spenta
   uscendo dalla scheda — tenerla viva per tutto il viaggio è batteria bruciata
   per un dato che nessuno sta guardando. */
function avviaPosizione() {
  if (guardiaGPS !== null || !navigator.geolocation) return;
  guardiaGPS = navigator.geolocation.watchPosition(
    (p) => {
      gpsConcesso = true;
      posizione = {
        lat: p.coords.latitude, lon: p.coords.longitude,
        metri: p.coords.accuracy, quando: Date.now(),
      };
      aggiornaPosizione();
    },
    (e) => {
      // Un errore non cancella l'ultima posizione buona: in galleria il GPS
      // cade, e "dov'eri un minuto fa" vale più di niente. Lo dichiara vecchio
      // chi lo disegna.
      if (!posizione || posizione.errore) {
        posizione = { errore: e.code === e.PERMISSION_DENIED
          ? 'Permesso negato: si riattiva dalle impostazioni del telefono.'
          : 'Posizione non disponibile adesso.' };
      }
      aggiornaPosizione();
    },
    { enableHighAccuracy: true, maximumAge: 15_000, timeout: 20_000 },
  );
}

/* Accende il GPS aprendo la scheda, ma solo dove non costa una richiesta di
   permesso: chi l'ha già dato non deve toccare niente, chi non l'ha dato non
   si vede comparire una finestra di sistema che non ha chiesto. Per lui resta
   il bottone, e il permesso si chiede dentro il tocco. */
async function avviaSePermesso() {
  if (guardiaGPS !== null || !navigator.geolocation) return;
  if (!(await permessoDato())) return;
  // Nel frattempo si può essere usciti dalla scheda: accendere il GPS adesso
  // vorrebbe dire lasciarlo acceso su una vista che non lo guarda, che è
  // proprio quello che `fermaPosizione` era lì per evitare.
  if (leggiRotta().vista !== 'treno') return;
  avviaPosizione();
  // La scheda è già stata disegnata mentre si aspettava la risposta: senza
  // questo la mappa comparirebbe solo alla lettura dopo.
  aggiornaVista();
}

function fermaPosizione() {
  if (guardiaGPS === null) return;
  navigator.geolocation.clearWatch(guardiaGPS);
  guardiaGPS = null;
}

/* Una lettura del GPS aggiorna la riga della distanza e la mappa, e nient'altro.

   Prima rifaceva la pagina intera, intestazione compresa, a ogni posizione — su
   un treno in corsa una al secondo. Su iOS un tocco diventa un click solo se
   il nodo toccato è ancora nella pagina quando il dito si alza, e il tasto per
   tornare in home veniva ricostruito da capo sotto il dito: lo si toccava e
   non succedeva niente. Adesso il resto della scheda non si muove, e la pagina
   non salta nemmeno mentre la si sta leggendo.

   Fuori dalla scheda del treno non si disegna niente: una lettura arrivata
   dopo essere usciti riguarda una vista che non c'è più. */
function aggiornaPosizione() {
  const r = leggiRotta();
  if (r.vista !== 'treno') return;
  const v = viaggiSeguiti.get(chiaveTreno(r.treno));
  const d = v && v.stato === 'ok' ? v.dati : null;
  if (!d) return;
  const riga = $('.riga-gps');
  if (riga) riga.outerHTML = rigaPosizione(d);
  if (mappa && mappa.el.isConnected) aggiornaMappa(mappa, d);
}

/* Quanti metri fra due punti, sulla sfera.

   La formula dell'emisenoverso: su distanze da pochi chilometri qualsiasi
   approssimazione andrebbe, ma questa costa cinque righe e non ha un limite
   oltre il quale sbaglia. */
function metriFra(aLat, aLon, bLat, bLon) {
  const R = 6371000, rad = Math.PI / 180;
  const p1 = aLat * rad, p2 = bLat * rad;
  const dp = p2 - p1, dl = (bLon - aLon) * rad;
  const x = Math.sin(dp / 2) ** 2 + Math.cos(p1) * Math.cos(p2) * Math.sin(dl / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(x));
}

/* Quanto dista una fermata da dove sei, in linea d'aria.

   Le fermate senza coordinate danno null invece di finire a zero gradi, che è
   in mezzo al golfo di Guinea e a duemila chilometri da qualsiasi treno. */
function distanzaDa(f) {
  if (!f || !f.lat || !f.lon || !posizione || posizione.errore) return null;
  return metriFra(posizione.lat, posizione.lon, f.lat, f.lon);
}

/* La prima fermata davanti al treno, fra quelle che si possono misurare.

   Non la fermata *più vicina*, che era la versione di prima e diceva la cosa
   sbagliata proprio nel momento in cui la si guardava: appena servita una
   fermata il treno le resta accanto per qualche chilometro, e la più vicina
   restava quella — la stazione lasciata dietro, con la distanza che cresceva
   a ogni lettura invece di scendere. La domanda è "quanto manca alla mia", e
   ha una risposta sola: quello che c'è davanti.

   Il "davanti" è `passed`, cioè l'orario reale delle fermate, la stessa cosa
   su cui si regola l'elenco qui sotto: così la riga e la lista non possono
   raccontare due viaggi diversi. */
const fermataProssima = (d) => (d.stops || []).find((f) => !f.passed && f.lat && f.lon) || null;

/* Dove si scende.

   È la fermata scelta sul tabellone da cui si è seguito il treno — quella che
   la lista accende — e in mancanza l'ultima del viaggio, che è il capolinea
   scritto in cima alla scheda. Sapere che alla prossima mancano due chilometri
   dice quando alzarsi; sapere che alla propria ne mancano venti dice se c'è
   tempo per un caffè. */
function fermataArrivo(d) {
  const con = (d.stops || []).filter((f) => f.lat && f.lon);
  return con.find((f) => f.chosen) || con[con.length - 1] || null;
}

// Se almeno una fermata ha le coordinate: senza, non c'è niente da misurare
// né da mettere su una mappa.
const haCoordinate = (d) => (d.stops || []).some((f) => f.lat && f.lon);

/* La distanza scritta come la si dice: in metri arrotondati a cinquanta finché
   ci stanno, poi in chilometri con un decimale. "1348 m" è una precisione che
   il GPS non ha e che a nessuno serve.

   La soglia guarda il numero *arrotondato* e non quello vero: guardando quello
   vero, a 950 metri si passava a "0,9 km" — attraversando la soglia il numero
   sembrava diminuire, che è il genere di cosa che fa dubitare di tutto il
   resto. Così invece 975 metri diventano "1,0 km" e la scala non torna mai
   indietro.

   Sotto i cinquanta metri non si scrive una cifra: il GPS ha una precisione di
   decine di metri, e "0 m" sarebbe una misura che non ha. */
function distanzaScritta(m) {
  if (m < 50) return 'meno di 50 m';
  const arrotondati = Math.round(m / 50) * 50;
  if (arrotondati < 1000) return `${arrotondati} m`;
  return `${(m / 1000).toFixed(1).replace('.', ',')} km`;
}

/* Dov'è adesso, detto per esteso. Sta su una riga sua, sotto tutto il resto:
   è una frase, non un dato incolonnato, e spezzata in mezzo agli altri campi
   si leggerebbe peggio. */
function doveAdesso(d, lettoIl) {
  if (d.arrived) return d.terminus ? `arrivato a ${esc(titolo(d.terminus))}` : 'arrivato';
  const l = d.lastSeen || {};
  // L'età anche prima della partenza: è lì, in banchina, che si guarda la
  // scheda per il ritardo e il binario, e una lettura di cinque minuti fa che
  // non lo dice sembra quella di adesso.
  if (!d.tracked || !l.station) return `non ancora partito${etaLettura(lettoIl)}`;
  return `rilevato a ${esc(titolo(l.station))}${l.time ? ` alle ${esc(l.time)}` : ''}${etaLettura(lettoIl)}`;
}

function etaLettura(lettoIl) {
  if (!lettoIl || Date.now() - lettoIl < VECCHIA) return '';
  return ` · letto ${fa(Date.now() - lettoIl)}`;
}

/* Il numero del binario, con la qualifica staccata quando ce n'è una.

   A Milano Porta Garibaldi RFI manda "2 SOT": il binario è il 2, "SOT" dice
   che è nei sotterranei — che sono cinque minuti di camminata dai binari di
   superficie, quindi non è una cosa da togliere. Ma scritta grande quanto la
   cifra allargava la colonna su quasi ogni suburbano, e la colonna ballava da
   una riga all'altra.

   La cifra resta il pezzo grande, la qualifica scende di misura e resta lì
   accanto. Quello che non combacia con "numero più resto" si scrive tale e
   quale: RFI in quella casella ci mette di tutto, e non vale la pena
   indovinare. */
function numeroBinario(p) {
  const m = /^(\d+)\s+(.+)$/.exec(p);
  return m ? `${esc(m[1])}<small>${esc(m[2])}</small>` : esc(p);
}

/* La cella del binario.

   Sotto la cifra non c'è più scritto "bin.": in una colonna che sta accanto a
   un'ora e a una destinazione, un numero dentro una pastiglia è il binario, e
   dirlo sotto ogni riga di ogni tabellone è una parola che nessuno legge due
   volte. Resta per chi ascolta la pagina, dove un "2" da solo non vuol dire
   niente.

   La didascalia ricompare quando ha qualcosa da dire che il numero non dice:
   "era 3" a binario cambiato. È il caso per cui quello slot esiste davvero, ed
   è anche il motivo per cui non l'ho tolto del tutto. */
function cellaBinario(platform, cambio) {
  if (!platform) {
    return '<div class="binario">' +
      '<span class="ignoto" title="Binario non ancora assegnato">–</span></div>';
  }
  return `<div class="binario${cambio ? ' cambiato' : ''}">
    <span class="solo-lettori">binario</span>
    <span class="num">${numeroBinario(platform)}</span>
    ${cambio ? `<span class="cap">${esc(cambio)}</span>` : ''}
  </div>`;
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

/* La riga di tabellone di un treno seguito.

   Finché il treno è sul tabellone della stazione da cui lo si segue, è quella
   vera, servita dal server: porta le due letture del ritardo, il soppresso, il
   binario cambiato e l'ora di arrivo a destinazione, tutto già unito. È il
   motivo per cui la scheda in home e la riga in elenco ora si somigliano —
   sono lo stesso pezzo, non due che si assomigliano.

   Partito, dal tabellone sparisce, e la lettura di RFI diventa quella che la
   prossima fermata mostra sui suoi arrivi — la riga si compone qui, con l'ora
   e il binario del viaggio, perché quella di un tabellone arrivi dice l'ora e
   la provenienza di un altro posto. Se nemmeno quella c'è resta la misura sul
   treno da sola, dichiarando che del tabellone non si sa niente: inventare uno
   zero ambra vorrebbe dire far dire a RFI che l'ha visto in orario. */
function rigaSeguita(d) {
  if (d.row) return d.row;

  const fermate = d.stops || [];
  const salita = fermate.find((f) => f.boarding) || fermate[0] || {};
  const prossima = prossimaFermata(d);
  const scesa = fermate.find((f) => f.chosen);
  const binario = prossima || salita;
  const rfi = d.nextRow || {};
  return {
    senzaRFI: !d.nextRow,
    delay: rfi.delay,
    status: rfi.status,
    cancelled: rfi.cancelled,
    number: d.number,
    category: d.category,
    terminus: d.terminus,
    // L'ora è quella della fermata da cui si sale, non del capolinea da cui il
    // treno viene: su un intercity preso a Rogoredo sono due cose diverse. Ed è
    // quella prevista, come su ogni riga di tabellone: il ritardo lo dice la
    // misura accanto, e un'ora già spostata più un +7 sarebbe contarlo due
    // volte.
    time: salita.scheduled || '',
    liveDelay: d.tracked ? (d.delay || 0) : undefined,
    platform: binario.platform || '',
    platformChanged: !!binario.platformScheduled,
    platformScheduled: binario.platformScheduled,
    platformActual: binario.platform,
    arrival: scesa ? (scesa.scheduled || '') : '',
  };
}

/* La scheda di un treno seguito in home: la cosa più grande della pagina, ed è
   giusto così — è il treno che si sta prendendo.

   Non è più la riga del tabellone con una riga in più sotto. Quella stava in
   home e sul tabellone con la stessa misura, e in home era la risposta a una
   domanda sola — a che ora, da che binario, quanto manca — scritta piccola
   come le quaranta righe di un tabellone. Qui l'ora e il binario sono grandi,
   e accanto alle due letture del ritardo c'è il conto che si farebbe a mente:
   fra quanti minuti parte davvero. I dati sono gli stessi, li compone sempre
   rigaSeguita(). */
function schedaSeguito(t) {
  const v = viaggiSeguiti.get(chiaveTreno(t));
  const d = v && v.stato === 'ok' ? v.dati : null;
  const riga = d ? rigaSeguita(d) : null;
  const link = rottaTreno(t);
  const nome = esc([riga ? riga.category : t.cat, riga ? riga.number : t.n]
    .filter(Boolean).join(' ')) || 'treno';
  const alto = `<div class="scheda-alto"><b>${nome}</b>${
    t.f ? `<span>da ${esc(nomeStazione(t.f))}</span>` : ''}</div>`;

  // Finché il viaggio non è arrivato la scheda dice comunque di che treno si
  // tratta: l'etichetta è quella con cui la si è salvata, e senza sarebbe una
  // scheda vuota proprio all'apertura dell'app.
  if (!riga) {
    const nota = v && v.stato === 'errore'
      ? 'posizione non disponibile adesso' : 'cerco dov\'è il treno…';
    return `<li><a class="scheda" href="${link}">${alto}
      <div class="scheda-mezzo"><span class="ora">–</span>
        <span class="scheda-dest">${esc(titolo(t.capolinea || ''))}</span></div>
      <div class="scheda-dove">${nota}</div>
    </a></li>`;
  }

  const classi = ['scheda'];
  if (riga.cancelled) classi.push('soppresso');
  else if (ritardoVero(riga) > 0) classi.push('in-ritardo');
  if (d.arrived) classi.push('concluso');
  const cambio = cambioBinario(riga);
  return `<li><a class="${classi.join(' ')}" href="${link}">
    ${fasciaProvvedimento(d)}
    ${alto}
    <div class="scheda-mezzo">
      <span class="ora${riga.cancelled ? ' barrato' : ''}">${esc(riga.time || '–')}</span>
      <span class="scheda-dest">${esc(titolo(riga.terminus || ''))}</span>
      <span class="scheda-bin${cambio ? ' cambiato' : ''}">
        <b>${riga.platform ? numeroBinario(riga.platform) : '–'}</b>
        <small>${cambio ? esc(cambio) : 'binario'}</small>
      </span>
    </div>
    <div class="scheda-basso">${scarti(riga, false)}${
      riga.arrival ? `<span>arrivo ${esc(riga.arrival)}</span>` : ''}${fraQuanto(t, d, riga)}</div>
    <div class="scheda-dove${d.tracked && !d.arrived ? ' vivo' : ''}">${doveAdesso(d, v.lettoIl)}</div>
  </a></li>`;
}

/* Fra quanto parte dalla stazione da cui si sale, con il ritardo già dentro:
   è il conto che si fa a mente guardando l'ora e il +3, fatto una volta per
   tutti e rifatto a ogni minuto.

   Solo entro l'ora: più in là l'ora scritta basta, e "fra 214 min" è un numero
   da convertire. E mai a treno partito dalla fermata di salita, che è quando
   la riga sotto dice dov'è — un "fra 1 min" su un treno già andato sarebbe
   l'errore che fa correre per niente.

   Il giorno è la mezzanotte di Roma della partenza dall'origine, come in
   abitualiDiOggi, e ne eredita il limite: un treno che passa dalla fermata di
   salita dopo mezzanotte avrebbe il giorno sbagliato, e qui il conto tace. */
function fraQuanto(t, d, riga) {
  if (riga.cancelled || d.arrived || !riga.time) return '';
  const salita = (d.stops || []).find((f) => f.boarding);
  if (salita && salita.passed) return '';
  const minuti = fraMinuti(riga.time, ritardoVero(riga), Date.now(), t.d);
  return minuti ? `<span class="fra">fra ${minuti} min</span>` : '';
}

/* La scheda di un treno seguito, aperta a tutta pagina: sopra le stesse tre
   cose della riga in home, sotto il viaggio intero con il ritardo a cui è
   passato dalle fermate già servite e il binario di ognuna. */
function disegnaTreno(t) {
  const k = chiaveTreno(t);
  const v = viaggiSeguiti.get(k);
  const d = v && v.stato === 'ok' ? v.dati : null;
  const salvato = eSeguito(t);
  // Da seguire si prende il viaggio quando c'è, perché porta l'etichetta
  // giusta; altrimenti quello che si era salvato, e in ultimo la sola rotta.
  // La destinazione però viene sempre dal segnalibro: qui non c'è un tabellone
  // da cui leggerla, e ripremere la stella non deve perderla.
  //
  // Il treno abituale di oggi conta come segnalibro per la stazione di salita,
  // ma il tasto resta spento: non è salvato, e premerlo lo salva davvero — con
  // la stessa chiave, quindi la scheda in home resta una.
  const segnalibro = seguitiInHome().find((x) => chiaveTreno(x) === k);
  //
  // Un viaggio senza `id` è il treno che ViaggiaTreno non traccia ancora — il
  // server risponde `tracked: false` e basta — e vale come nessun viaggio: lì
  // daSeguire() lanciava, e la scheda restava ferma senza mai far partire il
  // timer.
  const noto = segnalibro || t;
  const oggetto = d && d.id ? daSeguire(d, noto.a, noto.f) : noto;
  // Il nome, finché il viaggio non lo porta, è quello con cui lo si è salvato:
  // la rotta da sola ha il numero e basta, e un treno non tracciato restava
  // "24854" senza categoria né capolinea.
  const nomeSalvato = { category: noto.cat, number: noto.n, terminus: noto.capolinea };

  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">${etichettaTreno(d && d.id ? d : nomeSalvato)}</h1>
      <button class="tasto" type="button" data-segui="${esc(JSON.stringify(oggetto))}"
              aria-pressed="${salvato}"
              aria-label="${salvato ? 'Smetti di seguire questo treno' : 'Segui questo treno'}"
              >${icona('segnalibro', salvato)}</button>
    </div>
    ${sottotitolo(d && d.origin ? `da ${esc(titolo(d.origin))}` : '', rigaFreschezza())}
    ${barraCiclo()}`;

  if (!d) {
    app.innerHTML = v && v.stato === 'errore'
      ? '<p class="errore">Il viaggio di questo treno non è disponibile adesso.</p>'
      : `<ul>${scheletro().repeat(3)}</ul>`;
    return;
  }

  app.innerHTML = `
    ${sommarioTreno(d, v.lettoIl)}
    ${fasciaProvvedimento(d)}
    ${rigaPosizione(d)}
    ${sezioneMappa(d)}
    ${d.stops && d.stops.length
      ? elencoFermate(d, 'aperta', true)
      : '<p class="nota">ViaggiaTreno non pubblica le fermate di questo treno.</p>'}
    ${segnalibro ? '' : `<p class="nota">Non stai seguendo questo treno: tocca il segnalibro
      in alto per tenerlo in cima alla home.</p>`}`;
  // Dopo l'innerHTML, perché la mappa va agganciata a un posto che esiste: il
  // nodo è quello di prima, spostato, non uno nuovo.
  if (conMappa(d)) montaMappa(d, chiaveTreno(t));
}

/* In cima alla scheda del treno, la risposta alla prima domanda che si fa da
   sopra un treno: di quanto è in ritardo, e da dove viene la lettura. Il
   numero grande è quello che conta — la misura sul treno quando c'è — e sotto
   restano le due pastiglie, perché le due fonti possono non dire la stessa
   cosa e la scheda non sceglie al posto di chi guarda.

   Dov'è il treno non si scrive qui: lo disegna la linea delle fermate sotto. */
function sommarioTreno(d, lettoIl) {
  const riga = rigaSeguita(d);
  const r = riga.cancelled ? null : ritardoVero(riga);
  const l = d.lastSeen || {};
  const titoloSommario = d.arrived ? 'Arrivato'
    : (riga.cancelled ? 'Soppresso' : (d.tracked && l.station ? 'Ultimo rilevamento' : 'Non ancora partito'));
  const dove = d.tracked && l.station
    ? `${esc(titolo(l.station))}${l.time ? ` alle ${esc(l.time)}` : ''}${etaLettura(lettoIl)}` : '';
  return `<section class="sommario${riga.cancelled ? ' soppresso' : ''}">
    <span class="rit ${r === null ? '' : versoRitardo(r)}">${r === null ? '–' : esc(segnoRitardo(r))}<small>minuti</small></span>
    <b>${titoloSommario}</b>
    <span class="dove-sommario">${dove}</span>
    <span class="fonti">${scarti(riga, false)}</span>
  </section>`;
}

/* La mappa sta dietro a un tab, chiuso.

   Non perché occupi spazio, ma perché è lei a volere la posizione: montata da
   sola all'apertura della scheda, chiedeva il permesso a chi aveva aperto
   l'app per guardare a che ora passa il treno. Aperta a mano, il permesso si
   chiede dentro quel tocco — dove ha anche una ragione visibile.

   Il tab c'è solo dove la mappa avrebbe qualcosa da disegnare: senza nemmeno
   una fermata con le coordinate resterebbe un riquadro vuoto da aprire. */
function sezioneMappa(d) {
  if (!navigator.geolocation) return '';
  if (!haCoordinate(d)) return '';
  return `<button class="tab-mappa" type="button" data-mappa aria-expanded="${mappaAperta}">
      ${icona('mira')}<span>Mappa del viaggio</span>
      <span class="chevron">${icona('gallone')}</span>
    </button>
    ${mappaAperta ? '<div id="posto-mappa"></div>' : ''}`;
}

// La mappa è montata quando il tab è aperto. Le coordinate si ricontrollano
// perché il viaggio si rilegge ogni minuto, e quello nuovo potrebbe non
// averne.
function conMappa(d) {
  return mappaAperta && haCoordinate(d);
}

/* Quanto manca alla tua fermata, secondo il telefono.

   Il permesso si chiede dentro il tocco: su iOS una richiesta fatta dopo un
   `await` non conta più come gesto dell'utente, ed è la stessa regola per cui
   la campanella chiede il permesso nel gestore del click. Quindi qui c'è un
   bottone, non una richiesta automatica all'apertura della scheda — che
   sarebbe anche un permesso chiesto senza spiegare a cosa serve. */
function rigaPosizione(d) {
  if (!haCoordinate(d)) return '';

  if (!navigator.geolocation) return '';
  // Il bottone c'è ogni volta che il GPS non è acceso, e non solo la prima
  // volta in assoluto: su iOS il permesso vale una sessione, e a ogni riapertura
  // dell'app va ridato — dentro un tocco, che è l'unico posto in cui iOS lo
  // chiede. Prima lo si chiedeva da soli all'avvio, ed era la finestra di
  // sistema che compariva senza che nessuno avesse toccato niente.
  if (guardiaGPS === null) {
    return `<p class="riga-gps">
      <button class="btn-testo" type="button" data-gps>${icona('mira')} Quanto manca alla mia fermata</button>
    </p>`;
  }
  if (!posizione) {
    return '<p class="riga-gps attesa">cerco dove sei…</p>';
  }
  if (posizione.errore) {
    return `<p class="riga-gps attesa">${esc(posizione.errore)}</p>`;
  }
  // Due misure sulla stessa riga: la prossima fermata, che dice quando
  // alzarsi, e la propria, che dice quanto viaggio resta. Quando coincidono —
  // si scende alla prossima — se ne scrive una sola, che è anche il momento in
  // cui la riga conta di più e non deve dire due volte la stessa cosa.
  const prossima = fermataProssima(d);
  const arrivo = fermataArrivo(d);
  const pezzi = [];
  const mancano = distanzaDa(prossima);
  if (mancano !== null) {
    pezzi.push(`ti mancano <b>${esc(distanzaScritta(mancano))}</b> per ${esc(titolo(prossima.name))}`);
  }
  const allArrivo = arrivo && !arrivo.passed && arrivo !== prossima ? distanzaDa(arrivo) : null;
  if (allArrivo !== null) {
    pezzi.push(`<b>${esc(distanzaScritta(allArrivo))}</b> a ${esc(titolo(arrivo.name))}`);
  }
  // Davanti non c'è più niente: il treno è arrivato, o le fermate sono tutte
  // servite. Resta da dire dove sei rispetto all'ultima, e "mancano" lì
  // sarebbe la parola sbagliata.
  if (!pezzi.length) {
    const resta = distanzaDa(arrivo);
    if (resta !== null) pezzi.push(`sei a <b>${esc(distanzaScritta(resta))}</b> da ${esc(titolo(arrivo.name))}`);
  }
  if (!pezzi.length) return '<p class="riga-gps attesa">nessuna fermata di questo treno ha una posizione nota.</p>';
  return `<p class="riga-gps">${icona('mira')}
    <span>${pezzi.join(' · ')}</span>${etaPosizione()}</p>`;
}

/* L'età della posizione, ma solo quando è vecchia: in galleria il GPS cade, e
   un puntino di dieci minuti fa spacciato per adesso è la bugia peggiore su un
   treno in movimento. */
function etaPosizione() {
  if (!posizione || posizione.errore) return '';
  const s = Math.round((Date.now() - posizione.quando) / 1000);
  if (s < 90) return '';
  return `<em> · letta ${fa(s * 1000)}</em>`;
}

/* ------------------------------------------------------------------ mappa */

/* La mappa del viaggio: dove sei, e quali stazioni sono le tue.

   Scritta a mano invece che con una libreria perché lo zoom è fisso, e lo zoom
   è la parte difficile di una mappa: senza transizioni di scala e senza pinch
   resta un mosaico di quadrati da spostare, che sono queste cento righe.

   Lo zoom è fisso a 13 per una ragione misurata: più da lontano l'overlay
   ferroviario smette di disegnare i binari e i dischi delle stazioni diventano
   macchie da chilometri che si fondono fra loro. La sequenza completa delle
   fermate la dà l'elenco qui sotto, che per quello è più adatto di qualsiasi
   mappa. */
const MAPPA_Z = 13;
const TILE = 256;
const MAPPA_BASE = 'https://tile-a.openstreetmap.fr/hot/{z}/{x}/{y}.png';
const MAPPA_FERRO = 'https://a.tiles.openrailwaymap.org/standard/{z}/{x}/{y}.png';

/* Da gradi a pixel del mondo, secondo Mercatore. Sono le dieci righe che una
   libreria di mappe porta con sé assieme a tutto il resto. */
function proietta(lat, lon) {
  const n = TILE * 2 ** MAPPA_Z;
  return {
    x: (lon + 180) / 360 * n,
    y: (1 - Math.asinh(Math.tan(lat * Math.PI / 180)) / Math.PI) / 2 * n,
  };
}

/* La mappa viva, che sopravvive ai ridisegni.

   La scheda si ridisegna interamente a ogni lettura del viaggio e a ogni
   posizione nuova: ricreare la mappa ogni volta vorrebbe dire perdere lo
   scorrimento e ricaricare le tile. Il nodo quindi si tiene qui e a ogni
   ridisegno si *sposta* al suo posto — un nodo spostato conserva i figli, e
   con loro le immagini già scaricate. */
let mappa = null;

/* Se la mappa è aperta. Chiusa a ogni apertura di scheda, e non ricordata fra
   una sessione e l'altra: è il tab che tiene ferma la richiesta di permesso.
   Ricordata aperta, riaprendo l'app la mappa si rimonterebbe da sola e con lei
   tornerebbe la finestra di sistema, che è la cosa da cui si scappava. */
let mappaAperta = false;

function creaMappa(chiaveTreno) {
  const el = document.createElement('div');
  el.className = 'mappa';
  el.innerHTML = `
    <div class="mondo">
      <div class="strato base"></div>
      <div class="strato ferro"></div>
      <div class="segni"></div>
    </div>
    <button class="ricentra" type="button" data-ricentra
            aria-label="Torna sulla mia posizione">${icona('mira')}</button>
    <p class="attribuzione">© OpenStreetMap · ferrovie OpenRailwayMap</p>`;
  const m = {
    el, treno: chiaveTreno,
    mondo: el.querySelector('.mondo'),
    base: el.querySelector('.base'),
    ferro: el.querySelector('.ferro'),
    segni: el.querySelector('.segni'),
    tile: new Map(),   // "strato/x/y" -> img, per non riscaricare quel che c'è
    centro: null,      // pixel del mondo al centro del riquadro
    ancora: null,      // origine dei sistemi di riferimento interni
    seguiMe: true,     // finché non trascini, la mappa ti segue
  };
  collegaTrascinamento(m);
  return m;
}

/* Il trascinamento. Senza inerzia: il dito porta la mappa e la lascia dove la
   lascia. L'inerzia è la parte che su iOS va fatta sentire giusta, e si
   aggiunge se manca — non è un pezzo da cui dipende il resto. */
function collegaTrascinamento(m) {
  let da = null;
  m.el.addEventListener('pointerdown', (e) => {
    if (e.target.closest('[data-ricentra]')) return;
    da = { x: e.clientX, y: e.clientY, cx: m.centro.x, cy: m.centro.y };
    m.el.setPointerCapture(e.pointerId);
    m.el.classList.add('trascina');
  });
  m.el.addEventListener('pointermove', (e) => {
    if (!da) return;
    // Trascinando verso destra il mondo si sposta a destra, quindi il centro
    // va a sinistra: il segno è invertito.
    m.centro = { x: da.cx - (e.clientX - da.x), y: da.cy - (e.clientY - da.y) };
    m.seguiMe = false;
    posizionaMappa(m);
  });
  const fine = () => { da = null; m.el.classList.remove('trascina'); aggiornaRicentra(m); };
  m.el.addEventListener('pointerup', fine);
  m.el.addEventListener('pointercancel', fine);
}

/* Mette la mappa al suo posto nella pagina e la aggiorna.

   `d` è il viaggio: da lì vengono le fermate da cerchiare. Il centro lo decide
   la posizione se c'è, altrimenti la prossima fermata, altrimenti la prima —
   perché una mappa che si apre sul mare non dice niente a nessuno. */
function montaMappa(d, chiave) {
  const posto = $('#posto-mappa');
  if (!posto) return;
  if (!mappa || mappa.treno !== chiave) {
    mappa = creaMappa(chiave);
    mappa.centro = null;
  }
  posto.replaceWith(mappa.el);
  aggiornaMappa(mappa, d);
}

/* Aggiorna una mappa già in pagina: il centro, i segni, le tile. È la parte
   che una nuova posizione rifà da sola, senza rimontare niente. */
function aggiornaMappa(m, d) {
  const fuoco = posizione && !posizione.errore
    ? { lat: posizione.lat, lon: posizione.lon }
    : (prossimaFermata(d) || (d.stops || [])[0] || null);
  if (!fuoco || !fuoco.lat) return;
  const p = proietta(fuoco.lat, fuoco.lon);
  if (!m.centro) m.centro = { ...p };
  if (!m.ancora) m.ancora = { x: Math.round(p.x), y: Math.round(p.y) };
  if (m.seguiMe) m.centro = { ...p };

  disegnaSegni(m, d);
  posizionaMappa(m);
  aggiornaRicentra(m);
}

/* I dischi delle fermate e il puntino della posizione.

   Le fermate già servite non si segnano: marcare stazioni che hai lasciato
   dietro non aiuta nessuno, e togliendole la mappa respira. */
function disegnaSegni(m, d) {
  const pezzi = [];
  for (const f of d.stops || []) {
    if (!f.lat || !f.lon || f.passed) continue;
    const p = proietta(f.lat, f.lon);
    pezzi.push(`<div class="disco" style="left:${p.x - m.ancora.x}px;top:${p.y - m.ancora.y}px"></div>`);
  }
  if (posizione && !posizione.errore) {
    const p = proietta(posizione.lat, posizione.lon);
    pezzi.push(`<div class="io" style="left:${p.x - m.ancora.x}px;top:${p.y - m.ancora.y}px"></div>`);
  }
  m.segni.innerHTML = pezzi.join('');
}

/* Sposta il mondo e assicura le tile che servono.

   Il mondo è un unico nodo traslato: spostare la mappa è una sola proprietà
   che cambia, non venti posizioni ricalcolate. */
function posizionaMappa(m) {
  const W = m.el.clientWidth, H = m.el.clientHeight;
  if (!W || !H) return;
  const oX = m.centro.x - W / 2, oY = m.centro.y - H / 2;
  m.mondo.style.transform = `translate(${m.ancora.x - oX}px, ${m.ancora.y - oY}px)`;

  // Un quadrato di margine per lato: entrando in vista le tile sono già lì
  // invece di comparire dopo il dito.
  const x0 = Math.floor(oX / TILE) - 1, x1 = Math.floor((oX + W) / TILE) + 1;
  const y0 = Math.floor(oY / TILE) - 1, y1 = Math.floor((oY + H) / TILE) + 1;
  const serve = new Set();
  for (let x = x0; x <= x1; x++) {
    for (let y = y0; y <= y1; y++) {
      // Fuori dal mondo in verticale non c'è niente da chiedere; in
      // orizzontale si avvolge, ma a zoom 13 nessun treno italiano ci arriva.
      if (y < 0 || y >= 2 ** MAPPA_Z) continue;
      for (const [nome, url, dove] of [['b', MAPPA_BASE, m.base], ['f', MAPPA_FERRO, m.ferro]]) {
        const k = `${nome}/${x}/${y}`;
        serve.add(k);
        if (m.tile.has(k)) continue;
        const img = document.createElement('img');
        img.decoding = 'async';
        img.loading = 'eager';
        img.alt = '';
        img.style.left = `${x * TILE - m.ancora.x}px`;
        img.style.top = `${y * TILE - m.ancora.y}px`;
        img.src = url.replace('{z}', MAPPA_Z).replace('{x}', x).replace('{y}', y);
        dove.appendChild(img);
        m.tile.set(k, img);
      }
    }
  }
  // Quelle uscite di vista si buttano: tenerle tutte vorrebbe dire un nodo per
  // ogni quadrato d'Italia attraversato trascinando.
  for (const [k, img] of m.tile) {
    if (!serve.has(k)) { img.remove(); m.tile.delete(k); }
  }
}

function aggiornaRicentra(m) {
  const b = m.el.querySelector('[data-ricentra]');
  if (b) b.hidden = m.seguiMe || !posizione || !!posizione.errore;
}

/* Lo stato del mondo intorno ai propri treni, in cima alla home: una fila di
   gettoni che si scorre di lato, e sotto, quando se ne apre uno, il testo per
   intero.

   Prima erano tre cose una sotto l'altra — il cartello rosso degli scioperi,
   la striscia gialla degli avvisi che scorreva, la sezione dei bollini in
   fondo — e nei giorni storti spingevano il treno da prendere sotto la piega.
   Un gettone è una parola e un colore: dice che c'è qualcosa, e quanto è
   grave, nell'altezza di una riga. In una giornata normale ne resta uno solo,
   quello della rete.

   Gli scioperi stanno davanti agli avvisi per la stessa gerarchia di prima: il
   rosso dice "oggi il treno potrebbe non esserci", il giallo "guarda quando
   passi in stazione". */
function statoRete() {
  const scioperi = scioperiMiei();
  const avvisi = avvisiMiei();
  const voci = [];
  if (scioperi.size) {
    voci.push(`<button class="gettone sciopero" type="button" data-gettone="scioperi"
      aria-expanded="${scioperiAperti}">${icona('allerta')}Sciopero</button>`);
  }
  if (avvisi.size) {
    voci.push(`<button class="gettone stazione" type="button" data-gettone="avvisi"
      aria-expanded="${avvisiStazioneAperti}">${icona('allerta')}${esc(etichettaAvvisi(avvisi))}</button>`);
  }
  voci.push(...gettoniLinee());
  return `<div class="sezione stato-rete">
    <div class="gettoni">${voci.join('')}</div>
    ${scioperiAperti && scioperi.size ? elencoAvvisi(scioperi, 'sciopero', esc) : ''}
    ${avvisiStazioneAperti && avvisi.size ? elencoAvvisi(avvisi, '', (s) => esc(titolo(s))) : ''}
  </div>`;
}

/* Gli scioperi delle linee che segui, raggruppati sul testo: lo stesso
   sciopero è pubblicato su tutte le linee interessate, e uno sciopero
   nazionale altrimenti si scriverebbe quindici volte.

   Le comunicazioni arrivano dentro l'elenco delle linee, ma solo per le linee
   seguite: sono quelle che il servizio interroga per poter mandare le
   notifiche. Senza nessuna campanella accesa qui non c'è niente, ed è coerente
   — la stessa campanella che accende le notifiche accende il gettone. */
function scioperiMiei() {
  const miei = new Set(campanelle());
  const perTesto = new Map();
  for (const l of stato.linee || []) {
    if (!miei.has(l.code)) continue;
    for (const a of l.notices || []) {
      if (!a.strike) continue;
      if (!perTesto.has(a.text)) perTesto.set(a.text, new Set());
      perTesto.get(a.text).add(l.code);
    }
  }
  return perTesto;
}

/* Gli avvisi delle stazioni preferite, raggruppati sul testo come gli
   scioperi: un cantiere fra due fermate lo pubblicano tutt'e due, con lo
   stesso testo, e ripeterlo per stazione direbbe due volte una cosa sola. Il
   confronto è sul testo esatto: RFI lo scrive a mano, e due avvisi che dicono
   la stessa cosa con una parola diversa restano due avvisi.

   Si guarda anche che la stazione sia ancora fra i preferiti: togliendone uno
   la home si ridisegna subito, mentre gli avvisi in mano sono quelli
   dell'ultima richiesta. */
function avvisiMiei() {
  const miei = new Set(preferiti().map((p) => p.f));
  const perTesto = new Map();
  for (const s of stato.avvisiStazione || []) {
    if (!miei.has(s.placeId)) continue;
    for (const t of s.notices || []) {
      // Un Set e non una lista: se la stessa stazione ripete un avviso, il suo
      // nome non va scritto due volte nella stessa etichetta.
      if (!perTesto.has(t)) perTesto.set(t, new Set());
      perTesto.get(t).add(s.station);
    }
  }
  return perTesto;
}

// Il gettone dice dove, perché il dove decide se riguarda il viaggio di oggi;
// il cosa si legge aprendolo.
function etichettaAvvisi(perTesto) {
  const stazioni = new Set([...perTesto.values()].flatMap((s) => [...s]));
  if (stazioni.size > 1) return `Avvisi in ${stazioni.size} stazioni`;
  return `${perTesto.size > 1 ? 'Avvisi' : 'Avviso'} a ${titolo([...stazioni][0])}`;
}

/* Aperto, ogni avviso porta il nome della sua stazione — o il codice della
   linea, per gli scioperi: con due o tre preferiti su stazioni diverse, un
   testo senza etichetta non si sa a chi si riferisca. */
function elencoAvvisi(perTesto, classe, nome) {
  return `<ul class="elenco-avvisi-stazione ${classe}">${[...perTesto].map(([t, chi]) => `<li>
      <span class="stazione-avviso">${[...chi].map(nome).join(' · ')}</span>
      <span class="testo-avviso">${esc(t)}</span>
    </li>`).join('')}</ul>`;
}

/* Le linee nei gettoni: una per ogni linea seguita che ha qualcosa che non va,
   e in fondo il colpo d'occhio sulla rete intera.

   Le linee in difficoltà che uno non segue non ci sono mai state utili, e
   nemmeno le proprie quando vanno bene: "regolare" scritto tre volte è la
   risposta a una domanda che nessuno ha fatto. Il gettone di una linea porta
   all'elenco già aperto su di lei, che è dove si legge il perché.

   Quello della rete porta i numeri accanto ai bollini, come faceva la barra:
   il colore da solo non è un'informazione per tutti, e una linea grave su
   sessantacinque è proprio la cosa che deve vedersi. */
const ETICHETTE_CONTA = {
  regolare: 'regolari',
  critico: 'con criticità',
  grave: 'con gravi criticità',
  ignoto: 'senza stato',
};

function contaLinee(linee) {
  const conta = new Map();
  linee.forEach((l) => {
    const c = statoLinea(l.status).classe;
    conta.set(c, (conta.get(c) || 0) + 1);
  });
  const parti = ['regolare', 'critico', 'grave', 'ignoto'].filter((c) => conta.get(c));
  const detto = parti.map((c) => `${conta.get(c)} ${ETICHETTE_CONTA[c]}`).join(', ');
  return { parti, conta, detto };
}

/* Il codice di una linea come la si chiama — "S11", "RE13" — e il colore della
   sua famiglia. Il trattino basso è di Trenord, non di chi parla. I colori
   sono nostri: dicono la famiglia, non copiano la segnaletica. */
const codiceLinea = (l) => String(l.code).replace(/_/g, '');
const FAMIGLIE_LINEE = {
  'LINEE SUBURBANE': 's', 'REGIO EXPRESS': 're', REGIONALI: 'r',
  'MALPENSA EXPRESS': 'mxp', 'LINEE TRANSFRONTALIERE': 'tf',
};
const famigliaLinea = (l) => FAMIGLIE_LINEE[String(l.group || '').toUpperCase()] || '';

function gettoniLinee() {
  if (stato.linee === null) return [`<span class="gettone attesa">Linee</span>`];
  // Sottovoce: che manchino i bollini non deve sembrare che sia rotto il
  // tabellone, che è l'unica cosa per cui l'app si apre di corsa.
  if (stato.lineeErrore) {
    return [`<a class="gettone attesa" href="${ROTTA_LINEE}">Stato linee non disponibile</a>`];
  }
  const mie = stato.linee.filter((l) => seguita(l.code) && l.status > 0).map((l) => {
    const st = statoLinea(l.status);
    return `<a class="gettone" href="${ROTTA_LINEE}/${encodeURIComponent(l.code)}">
      <span class="bollino ${st.classe}"></span>${esc(codiceLinea(l))} ${st.etichetta}</a>`;
  });
  const { parti, conta, detto } = contaLinee(stato.linee);
  return [...mie, `<a class="gettone" href="${ROTTA_LINEE}"${detto ? ` aria-label="Linee: ${esc(detto)}"` : ''}>
    Linee${parti.map((c) => `<span class="conta"><span class="bollino ${c}"></span>${conta.get(c)}</span>`).join('')}</a>`];
}

function rigaLinea(l, query) {
  const st = statoLinea(l.status);
  const accesa = seguita(l.code);
  const campanella = `<button class="campanella${accesa ? ' accesa' : ''}" type="button"
      data-campanella="${esc(l.code)}" aria-pressed="${accesa}"
      aria-label="${accesa ? 'Smetti di seguire' : 'Segui'} ${esc(l.name)}"
      >${icona('campana', accesa)}</button>`;
  // Il perché di un bollino acceso si legge senza aprire la riga, quando le
  // comunicazioni ci sono già — le linee seguite arrivano con le loro. Aperta,
  // l'elenco intero sta sotto e la prima riga non va ripetuta.
  const v = avvisiLinea.get(l.code);
  const primo = l.status > 0 && !lineeAperte.has(l.code) && v && v.stato === 'ok'
    ? diCircolazione(v.dati)[0] : null;
  const nome = `<span class="codice-linea ${famigliaLinea(l)}">${esc(codiceLinea(l))}</span>
    <span class="testo">${evidenzia(l.name, query)}
      <span class="stato-linea ${st.classe}">${st.etichetta}</span>
      ${primo ? `<span class="perche">${esc(primo.text)}</span>` : ''}</span>`;

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
  const dati = diCircolazione(v.dati);
  if (!dati.length) {
    return '<p class="avvisi-attesa">Nessuna comunicazione su questa linea.</p>';
  }
  return `<ol class="avvisi">${dati.map(vociAvviso).join('')}</ol>`;
}

/* Quello che sta succedendo adesso, dal più recente.

   La pagina di Trenord tiene due elenchi: "STATO DELLA LINEA", che è la
   circolazione di oggi, e "AVVISI", che è il programmato — variazioni d'orario
   fino a dicembre, scioperi, i PDF. Arrivano mescolati in una lista sola, e
   mescolati non si leggono: le tre righe che dicono cosa sta succedendo alla
   tua linea stasera finivano in mezzo a due cartelli di settembre.

   Il programmato resta comunque leggibile dove vale: la striscia gialla in
   cima alla home, che è per i cartelli e non per la circolazione.

   Una sezione che non conosciamo si tiene: se la sorgente cambia sotto, meglio
   una riga in più che una notizia scomparsa in silenzio. */
const SEZIONE_AVVISI = 1;

function diCircolazione(avvisi) {
  return avvisi
    .filter((a) => a.section !== SEZIONE_AVVISI)
    // Trenord non li manda in ordine: la RE_5 dell'8 settembre li dava 18:41,
    // 18:46, 18:42. In cima va quello che vale adesso.
    .sort((a, b) => quandoAvviso(b) - quandoAvviso(a));
}

const quandoAvviso = (a) => {
  const d = a.date ? new Date(a.date) : null;
  // Senza data va in fondo, non in cima: non si sa quando sia stato scritto, e
  // non è il candidato a essere il più recente.
  return d && !isNaN(d) ? d.getTime() : -Infinity;
};

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
    const d = await leggiJSON(API.avvisiLinea(codice), { signal: AbortSignal.timeout(15_000) });
    if (!d) return;   // la pagina si sta ricaricando: non c'è niente da ridisegnare
    avvisiLinea.set(codice, { stato: 'ok', dati: d.notices || [] });
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
  return `<li>
    ${quandoScritto(a) ? `<span class="data-avviso">${esc(quandoScritto(a))}</span>` : ''}
    <span class="testo-avviso">${esc(a.text)}</span>
  </li>`;
}

/* Quando è stata scritta, con l'ora.

   Senza l'ora non si leggeva: le tre comunicazioni di una sera portano tutte
   "8 settembre" e diventano indistinguibili, mentre la differenza fra quella
   delle 18:41 e quella delle 18:46 è tutta la notizia. Di oggi si scrive la
   sola ora — il giorno lo si sa — e dei giorni prima il giorno e l'ora, come
   fa Trenord. */
function quandoScritto(a) {
  // La data si legge come per l'ordine: senza, o illeggibile, è -Infinity.
  const ms = quandoAvviso(a);
  if (ms === -Infinity) return '';
  const d = new Date(ms);
  const ora = d.toLocaleTimeString('it-IT', { hour: '2-digit', minute: '2-digit' });
  if (d.toDateString() === new Date().toDateString()) return ora;
  return `${d.toLocaleDateString('it-IT', { day: 'numeric', month: 'long' })}, ${ora}`;
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
  const giorni = bottoniGiorni(f.giorni, (v) => `data-giorno="${i}:${v}"`);
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

/* I sette bottoni dei giorni, per una fascia e per un treno abituale: cambia
   solo l'attributo che dice al gestore del tocco di chi è quel giorno. */
const bottoniGiorni = (attivi, attr) => GIORNI.map((g) => {
  const acceso = (attivi || []).includes(g.v);
  return `<button class="giorno${acceso ? ' acceso' : ''}" type="button"
                    ${attr(g.v)} aria-pressed="${acceso}"
                    aria-label="${nomeGiorno(g.v)}">${g.l}</button>`;
}).join('');

// Un tocco su un giorno: spento se c'era, acceso se no, e i giorni in ordine.
function alternaGiorno(giorni, g) {
  const prima = giorni || [];
  return prima.includes(g) ? prima.filter((x) => x !== g) : [...prima, g].sort();
}

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

  if (!query) {
    dove.innerHTML = elencoLineeInOrdine();
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
      <h2 class="etichetta-sezione">${esc(titolo(g.nome))}</h2>
      <ul class="lista">${g.linee.map((l) => rigaLinea(l, query)).join('')}</ul>
    </section>`).join('');
}

/* L'elenco senza filtro, in ordine di quanto ti riguarda: prima le linee che
   segui, poi quelle che hanno un problema adesso, poi tutte le altre ripiegate
   per gruppo. Erano sessantacinque righe uguali nell'ordine di Trenord, e la
   linea che si era venuti a guardare stava a metà della terza schermata. */
function elencoLineeInOrdine() {
  const mie = stato.linee.filter((l) => seguita(l.code));
  const guai = stato.linee.filter((l) => !seguita(l.code) && l.status > 0);
  const altre = stato.linee.filter((l) => !seguita(l.code) && !(l.status > 0));
  const blocco = (titoloBlocco, linee) => (linee.length ? `<section class="sezione">
      <h2 class="etichetta-sezione">${titoloBlocco}</h2>
      <ul class="lista blocco">${linee.map((l) => rigaLinea(l, '')).join('')}</ul>
    </section>` : '');

  const gruppi = [];
  for (const l of altre) {
    const g = gruppi.find((x) => x.nome === l.group);
    if (g) g.linee.push(l); else gruppi.push({ nome: l.group, linee: [l] });
  }
  // Qui dentro ci sono solo linee senza problemi, che quelle stanno sopra:
  // il riassunto lo dice, invece di far aprire il gruppo per scoprirlo.
  const ripiegati = gruppi.map((g) => `<details class="gruppo-linee" data-gruppo="${esc(g.nome)}"${
    gruppiLineeAperti.has(g.nome) ? ' open' : ''}>
      <summary><b>${esc(titolo(g.nome))}</b><small>${g.linee.length} ${g.linee.length === 1 ? 'linea' : 'linee'}</small>
        <span class="riassunto">${g.linee.every((l) => l.status === 0)
          ? '<span class="bollino regolare"></span>tutte regolari' : ''}</span>
        <span class="chevron">${icona('gallone')}</span></summary>
      <ul class="lista">${g.linee.map((l) => rigaLinea(l, '')).join('')}</ul>
    </details>`).join('');

  return `${barraRete(stato.linee)}${blocco('Le tue linee', mie)}${blocco('Con problemi adesso', guai)}${
    ripiegati ? `<section class="sezione"><h2 class="etichetta-sezione">${
      mie.length || guai.length ? 'Tutte le altre' : 'Tutte le linee'}</h2>${ripiegati}</section>` : ''}`;
}

/* Il colpo d'occhio sulla rete intera, in cima all'elenco: la barra dice la
   proporzione e sotto ci sono i numeri scritti — il colore da solo non è
   un'informazione per tutti, e una fetta rossa larga tre pixel va comunque
   letta. Il minimo di larghezza è per lei: una linea grave su sessantacinque
   è l'unica cosa che questa barra deve far vedere. */
function barraRete(linee) {
  const { parti, conta, detto } = contaLinee(linee);
  if (!parti.length) return '';
  return `<div class="barra-linee" role="img" aria-label="${esc(detto)}">
      ${parti.map((c) => `<span class="${c}" style="flex:${conta.get(c)}"></span>`).join('')}
    </div>
    <p class="conta-linee" aria-hidden="true">${parti.map((c) =>
      `<span><span class="bollino ${c}"></span>${conta.get(c)} ${ETICHETTE_CONTA[c]}</span>`).join('')}</p>`;
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

/* La ricerca, in un pannello fermo in fondo allo schermo.

   I tre modi sono le due sezioni che la home aveva prima — il modulo per una
   tratta e le due tessere per il tabellone di una stazione — riunite in un
   posto solo: la domanda è sempre "da quale stazione", cambia solo cosa se ne
   vuole sapere. Il campo della stazione è lo stesso per tutti e tre, così
   cambiando modo non si riscrive niente.

   Sul tabellone intero la sigla resta, e dice la cosa giusta: "DA Monza" sono
   le partenze da Monza, "A Monza" gli arrivi a Monza. */
const MODI = [
  { v: 'tratta', l: 'Tratta', sigla: 'DA', vai: 'Vedi i treni' },
  { v: 'partenze', l: 'Partenze', sigla: 'DA', vai: 'Vedi le partenze' },
  { v: 'arrivi', l: 'Arrivi', sigla: 'A', vai: 'Vedi gli arrivi' },
];

function foglioRicerca() {
  const modo = MODI.find((m) => m.v === modoRicerca);
  const tratta = modo.v === 'tratta';
  return `<section class="foglio" aria-label="Cerca">
    <div class="segmenti" role="group" aria-label="Cosa cercare">${MODI.map((m) =>
      `<button type="button" data-modo="${m.v}" aria-pressed="${m.v === modo.v}">${m.l}</button>`).join('')}</div>
    <div class="gruppo">
      <div class="gruppo-campi">
        ${campoStazione('da', modo.sigla, stato.da, tratta ? 'Stazione di partenza' : 'Stazione')}
        ${tratta ? campoStazione('a', 'A', stato.a, 'Tutte le destinazioni') : ''}
      </div>
      ${tratta ? `<button class="inverti" type="button" data-scambia
              aria-label="Inverti partenza e arrivo">${icona('scambia')}</button>` : ''}
    </div>
    <button class="principale" type="button" data-vai ${stato.da ? '' : 'disabled'}>${modo.vai}</button>
  </section>`;
}

function campoStazione(quale, sigla, id, vuoto) {
  return `<button class="campo" type="button" data-apri="${quale}">
    <span class="sigla">${sigla}</span>
    <span class="valore ${id ? '' : 'vuoto'}">${id ? esc(nomeStazione(id)) : vuoto}</span>
    <span class="chevron">${icona('gallone')}</span>
  </button>`;
}

/* I treni abituali, sotto i preferiti: sono della stessa famiglia — cose che
   si salvano una volta e restano — e non dei treni seguiti, che sono di oggi.
   Il treno di oggi, quando è ora, compare in cima con gli altri seguiti.

   Chiusi sono una riga: l'ora, il treno, i giorni scritti. I sette bottoni dei
   giorni stavano sempre aperti, ed erano sette bersagli da pollice per una cosa
   che si cambia alle ferie; ora stanno sotto un tocco, insieme al modo di
   togliere il treno. */
function sezioneAbituali() {
  const elenco = abituali();
  if (!elenco.length) return '';
  return `<section class="sezione">
    <div class="testa-sezione"><h2 class="etichetta-sezione">Treni abituali</h2></div>
    <ul class="abituali">${elenco.map(rigaAbituale).join('')}</ul>
  </section>`;
}

function rigaAbituale(x) {
  const k = chiaveAbituale(x);
  const aperto = abitualiAperti.has(k);
  const testa = `<button class="abituale-testa" type="button" data-apri-abituale="${esc(k)}"
      aria-expanded="${aperto}">
      <span class="abituale-ora">${esc(x.at)}</span>
      <span class="abituale-testo">
        ${etichettaTreno({ category: x.cat, number: x.n, terminus: x.capolinea })}
        <span class="qualifica">da ${esc(nomeStazione(x.f))} · ${giorniScritti(x.days)}</span>
      </span>
      <span class="chevron">${icona('gallone')}</span>
    </button>`;
  if (!aperto) return `<li class="abituale">${testa}</li>`;

  const giorni = bottoniGiorni(x.days, (v) => `data-giorno-abituale="${esc(k)}:${v}"`);
  return `<li class="abituale">${testa}
    <div class="giorni" role="group" aria-label="Giorni del treno">${giorni}</div>
    <p class="abituale-piede">
      <span>${(x.days || []).length ? '' : 'Nessun giorno scelto: per ora non arriva.'}</span>
      <button class="btn-testo piccolo togli-testo" type="button" data-togli-abituale="${esc(k)}">
        Togli dai treni abituali</button>
    </p>
  </li>`;
}

/* I giorni come si dicono: "lun–ven" per il treno del lavoro, e i giorni uno
   per uno quando non sono un blocco che ha un nome. Nessun giorno è la pausa
   delle ferie, e si scrive così. */
function giorniScritti(giorni = []) {
  const g = new Set(giorni);
  if (!g.size) return 'in pausa';
  if (g.size === 7) return 'tutti i giorni';
  if (g.size === 5 && [1, 2, 3, 4, 5].every((v) => g.has(v))) return 'lun–ven';
  if (g.size === 2 && g.has(6) && g.has(0)) return 'sab e dom';
  return GIORNI.filter((x) => g.has(x.v)).map((x) => nomeGiorno(x.v).slice(0, 3)).join(' ');
}

function disegnaRisultati() {
  const d = stato.dati;
  const daNome = titolo((d && d.from) || (stato.da ? nomeStazione(stato.da) : ''));
  const aNome = titolo((d && d.to) || (stato.a ? nomeStazione(stato.a) : ''));
  const questa = { f: stato.da, t: stato.arrivi ? null : stato.a, a: stato.arrivi || undefined };
  const salvato = ePreferito(questa);

  const tratta = !!stato.a && !stato.arrivi;

  testa.innerHTML = `
    <div class="testa-riga">
      <a class="tasto" href="#/" aria-label="Torna alla home">${icona('indietro')}</a>
      <h1 class="titolo">${esc(daNome)}${tratta ? ` <span class="freccia">→</span> ${esc(aNome)}` : ''}</h1>
      <button class="tasto" type="button" data-preferito aria-pressed="${salvato}"
              aria-label="${salvato ? 'Togli dai preferiti' : 'Aggiungi ai preferiti'}">${icona('stella', salvato)}</button>
    </div>
    ${sottotitolo(tratta && d && d.trains.length
      ? `${d.trains.length} ${d.trains.length === 1 ? 'treno' : 'treni'} fino alle ${esc(d.trains[d.trains.length - 1].time)}`
      : '', rigaFreschezza())}
    ${barraCiclo()}`;

  /* Sul tabellone di una stazione partenze e arrivi sono le due facce della
     stessa cosa, e si passa dall'una all'altra senza tornare in home. Prima
     "Arrivi" era una parola nel sottotitolo — l'unico modo di sapere quale
     faccia si stava guardando — e per cambiarla si ripartiva dalla ricerca. */
  const facce = tratta ? '' : `<div class="segmenti facce" role="group" aria-label="Partenze o arrivi">
      <a href="${rottaDi(stato.da, null, false)}" aria-current="${!stato.arrivi}">Partenze</a>
      <a href="${rottaDi(stato.da, null, true)}" aria-current="${stato.arrivi}">Arrivi</a>
    </div>`;

  if (!d) {
    app.innerHTML = facce + (stato.errore
      ? `<p class="errore">${esc(stato.errore)}</p>`
      : `<ul>${scheletro().repeat(5)}</ul>`);
    return;
  }

  const note = [];
  if (stato.errore) note.push(`<p class="errore">${esc(stato.errore)} — mostrati gli ultimi dati ricevuti.</p>`);
  if (d.stopsUnavailable) {
    note.push(`<p class="nota">RFI non pubblica le fermate dei treni in arrivo:
      qui sotto ci sono tutti gli arrivi, senza il filtro per ${esc(aNome)}.</p>`);
  }

  if (!d.trains.length) {
    app.innerHTML = facce + note.join('') + `<div class="senza-risultati">
         <p>Nessun treno da ${esc(daNome)}${d.filtered ? ` che ferma a ${esc(aNome)}` : ''}.</p>
         <p>Il tabellone copre solo le prossime ore.</p>
       </div>`;
    return;
  }

  app.innerHTML = facce + note.join('') + legenda(d)
    + (d.filtered && !d.arrivals ? elencoTratta(d) : elencoStazione(d));
}

/* La tratta: il prossimo treno in grande, poi gli altri in un elenco solo, con
   un separatore a ogni cambio d'ora — su quaranta righe di orari simili è il
   segno che dice dove si è arrivati scorrendo. Il più veloce si dice, perché
   sulla stessa tratta un regionale veloce e un suburbano possono prendere un
   quarto d'ora di differenza, e dall'ora di partenza non si vede. */
function elencoTratta(d) {
  const misure = conMisure(d);
  const i = d.trains.findIndex((t) => !t.cancelled);
  const primo = i >= 0 ? d.trains[i] : null;
  const altri = d.trains.filter((t) => t !== primo);
  const durate = d.trains.filter((t) => !t.cancelled).map((t) => durata(t.time, t.arrival)).filter((x) => x !== null);
  const minima = durate.length > 1 && Math.min(...durate) < Math.max(...durate) ? Math.min(...durate) : null;
  const veloce = minima === null ? null : d.trains.find((t) => !t.cancelled && durata(t.time, t.arrival) === minima);

  let ora = null;
  const righe = altri.map((t) => {
    const h = String(t.time).slice(0, 2);
    const sep = ora !== null && h !== ora ? `<li class="ora-sep">ore ${Number(h)}</li>` : '';
    ora = h;
    return sep + rigaTreno(t, d, misure, t === veloce ? 'veloce' : '');
  }).join('');
  return `${primo ? `<ul>${rigaTreno(primo, d, misure, 'primo')}</ul>` : ''}
    ${righe ? `<ul class="tabella tratta">${righe}</ul>` : ''}`;
}

/* Il tabellone di una stazione: righe a filo come quelle del tabellone vero, il
   doppio dei treni per schermata delle schede di prima, e i filtri per
   famiglia quando ce n'è più di una. */
function elencoStazione(d) {
  const misure = conMisure(d);
  const presenti = GRUPPI_TRENO.filter(([g]) => d.trains.some((t) => gruppoTreno(t) === g));
  const filtro = presenti.some(([g]) => g === filtroTipo) ? filtroTipo : '';
  const filtri = presenti.length > 1 ? `<div class="filtri" role="group" aria-label="Che treni">
      <button type="button" data-tipo="" aria-pressed="${!filtro}">Tutti</button>
      ${presenti.map(([g, nome]) => `<button type="button" data-tipo="${g}" aria-pressed="${g === filtro}">${nome}</button>`).join('')}
    </div>` : '';
  const treni = filtro ? d.trains.filter((t) => gruppoTreno(t) === filtro) : d.trains;
  return `${filtri}<ul class="tabella">${treni.map((t) => rigaTreno(t, d, misure)).join('')}</ul>`;
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
/* Il ritardo secondo il tabellone. Zero quando RFI non scrive un numero, perché
   RFI il ritardo lo stampa comunque: la casella vuota vuol dire "in orario".

   Null invece quando una lettura del tabellone non c'è affatto — `senzaRFI` —
   che è il caso di un treno seguito già partito, sparito dal tabellone della
   stazione da cui lo si seguiva. Lì uno zero ambra sarebbe una puntualità che
   nessuno ha dichiarato. */
const ritardoRFI = (t) =>
  (t.cancelled || t.senzaRFI ? null : (typeof t.delay === 'number' ? t.delay : 0));
/* Il ritardo che conta per il colore dell'ora: la misura sul treno quando c'è,
   altrimenti quel che dice il tabellone. */
const ritardoVero = (t) => (ritardoLive(t) ?? ritardoRFI(t));

const segnoRitardo = (m) => (m > 0 ? `+${m}` : String(m));
/* Il verso del ritardo, per chi deve colorarlo. Serve solo dove il numero sta
   da solo: nelle due pastiglie il colore dice la fonte, e un rosso lì vorrebbe
   dire due cose in una. Un treno in anticipo non è una brutta notizia, e
   stamparlo rosso come i minuti persi era proprio la lettura sbagliata. */
const versoRitardo = (m) => (m > 0 ? 'tardi' : (m < 0 ? 'presto' : 'puntuale'));

function scarti(t, riservaVT) {
  if (t.cancelled) return '<span class="scarto solo">soppresso</span>';
  // RFI ogni tanto scrive nella cella del ritardo un testo invece di un numero
  // ("RITARDO", per un ritardo annunciato ma non ancora quantificato): quello
  // va riportato tale e quale, non tradotto in una cifra che non ha mandato.
  if (t.status) return `<span class="scarto solo">${esc(t.status.toLowerCase())}</span>`;

  const live = ritardoLive(t);
  // Senza la lettura del tabellone resta la sola misura sul treno, da sola: è
  // il treno seguito che è già partito, e a quel punto ViaggiaTreno lo sta
  // misurando. Se non misura nemmeno lui non si scrive niente, che è la verità.
  if (ritardoRFI(t) === null) {
    return live !== null
      ? `<span class="scarto misura ${versoRitardo(live)}" title="misurato sul treno"
         >${segnoRitardo(live)}</span>`
      : '';
  }
  // Posto vuoto al posto della misura mancante: senza, la pastiglia di RFI
  // scivolerebbe a destra, proprio dove sulle altre righe c'è quella di
  // ViaggiaTreno. Il posto si riserva solo se in lista una misura c'è: quando
  // non ne ha nessuno sarebbe una colonna vuota per tutto il tabellone.
  const seconda = live !== null
    ? `<span class="scarto vt" title="misurato sul treno">${segnoRitardo(live)}</span>`
    : (riservaVT ? '<span class="scarto vt vuota"></span>' : '');
  return `<span class="scarti">
    <span class="scarto rfi" title="tabellone RFI">${segnoRitardo(ritardoRFI(t))}</span>
    ${seconda}
  </span>`;
}

/** Se in lista almeno un treno è stato rilevato, la seconda colonna esiste. */
const conMisure = (d) => d.trains.some((t) => ritardoLive(t) !== null);

/* La legenda compare solo se almeno un treno porta la misura di ViaggiaTreno:
   con la sola colonna di RFI non ci sarebbero due colori da spiegare.

   È una riga sola, con i due colori e il loro nome, e la spiegazione intera
   sta dietro un tocco. Prima era un riquadro di quattro righe con un "Ho
   capito" per chiuderlo: spiegava un codice che si impara una volta, ma stava
   sopra ogni tabellone nel posto della prima partenza — e chi lo chiudeva
   perdeva per sempre il modo di ricordarsi quale colore fosse quale. */
function legenda(d) {
  if (!conMisure(d)) return '';
  return `<details class="legenda">
    <summary><span class="scarto rfi campione"></span>tabellone RFI
      <span class="scarto vt campione"></span>sul treno
      <span class="info" aria-label="Cosa vogliono dire">${icona('info')}</span></summary>
    <p>Su alcuni treni il ritardo arriva da due parti, e non sempre dicono lo
      stesso numero: quello scritto sul tabellone di RFI, e quello misurato sul
      treno in corsa da ViaggiaTreno.</p>
  </details>`;
}

/* Che famiglia di treno è, per i filtri e per il segno colorato accanto al
   nome. Quattro famiglie e non le venti categorie di RFI: davanti al
   tabellone di Centrale la domanda è "il regionale o la Freccia", non "RV o
   REG". I colori sono nostri, non quelli dei vettori: dicono la famiglia, non
   la marca. */
const GRUPPI_TRENO = [
  ['reg', 'Regionali'], ['av', 'Alta velocità'], ['ic', 'Intercity'], ['int', 'Internazionali'],
];

function gruppoTreno(t) {
  const c = (t.category || '').trim().toUpperCase();
  if (c.startsWith('ALTA VELOCIT') || /ITALO|FRECCIA/.test((t.carrier || '').toUpperCase())) return 'av';
  if (/^(EC|EN|RJ|TGV|EUROCITY|EURONIGHT)$/.test(c)) return 'int';
  if (/^(IC|ICN|INTERCITY)/.test(c)) return 'ic';
  return 'reg';
}

/* Il nome del treno come lo si dice: "Frecciarossa 9551", "Regionale veloce
   2032". RFI scrive la categoria in sigla e la ripete come vettore — "ALTA
   VELOCITA' 9551 · FRECCIAROSSA" — e le due metà dicevano la stessa cosa in
   due modi illeggibili. Sull'alta velocità il nome è il vettore; sui regionali
   il vettore si aggiunge, perché a Milano sono di due aziende diverse e i
   biglietti non valgono sull'altra. Le sigle che la gente usa davvero — RE,
   S11 — restano sigle. */
const NOMI_CATEGORIA = {
  REG: 'Regionale', RV: 'Regionale veloce', IC: 'Intercity', INTERCITY: 'Intercity',
  ICN: 'Intercity Notte', 'INTERCITY NOTTE': 'Intercity Notte', EC: 'EuroCity', EN: 'EuroNight',
};

function servizioDi(t) {
  const c = (t.category || '').trim().toUpperCase();
  const nome = c.startsWith('ALTA VELOCIT')
    ? (t.carrier ? titolo(t.carrier) : 'Alta velocità')
    : (NOMI_CATEGORIA[c] || (t.category || '').trim());
  const pezzi = [[nome, t.number].filter(Boolean).join(' ')];
  if (gruppoTreno(t) === 'reg' && t.carrier && canon(t.carrier) !== 'trenitalia') pezzi.push(titolo(t.carrier));
  return pezzi.join(' · ');
}

/* Quanto dura il viaggio, dall'ora di partenza a quella di arrivo. Sono due
   "HH:MM" senza giorno: un arrivo che sembra prima della partenza è dopo
   mezzanotte. */
function durata(da, a) {
  if (!da || !a) return null;
  const d = minutiDi(a) - minutiDi(da);
  if (Number.isNaN(d)) return null;
  return d < 0 ? d + 24 * 60 : d;
}

/* Fra quanti minuti parte un treno, con il ritardo dentro. L'ora è un "HH:MM"
   di Roma. Il giorno, quando lo si sa — la mezzanotte di Roma della partenza
   di un treno seguito — è quello; sul tabellone, che non lo porta, è oggi, o
   domani se oggi è passata da più di sei ore — la stessa regola del server.
   Tace oltre l'ora, e a treno già partito: null. */
function fraMinuti(hhmm, ritardo, adesso = Date.now(), giorno = null) {
  const min = minutiDi(hhmm);
  if (Number.isNaN(min)) return null;
  let parte;
  if (giorno !== null) parte = istanteRoma(quadranteRoma(giorno) + min * 60_000);
  else {
    const q = new Date(quadranteRoma(adesso));
    const oggi = Date.UTC(q.getUTCFullYear(), q.getUTCMonth(), q.getUTCDate());
    parte = istanteRoma(oggi + min * 60_000);
    if (parte < adesso - 6 * 60 * 60_000) parte = istanteRoma(oggi + 24 * 60 * 60_000 + min * 60_000);
  }
  const minuti = Math.round((parte + Math.max(ritardo || 0, 0) * 60_000 - adesso) / 60_000);
  return minuti >= 1 && minuti <= 60 ? minuti : null;
}

/* Il binario cambiato: si dice da quale, non solo che è successo.

   Quale dei due numeri sia la novità dipende da chi è avanti fra le due fonti.
   Se il tabellone mostra già quello nuovo, la cosa da aggiungere è quello
   vecchio, per chi si è incamminato prima; se invece è rimasto indietro sul
   previsto, la cosa da aggiungere è quello nuovo. Fuori da questi due casi le
   due fonti dicono tre numeri diversi, e allora l'unica cosa onesta è dire che
   è cambiato senza pretendere di sapere in quale direzione. */
function cambioBinario(t) {
  if (!t.platformChanged) return null;
  if (t.platform === t.platformActual && t.platformScheduled) return `era ${t.platformScheduled}`;
  if (t.platform === t.platformScheduled && t.platformActual) return `ora ${t.platformActual}`;
  return 'cambiato';
}

/* L'orario con le letture del ritardo sotto: la prima colonna di ogni riga. */
const orarioDi = (t, misure) => `<div class="orario">
    <span class="ora ${t.cancelled ? 'barrato' : ''}">${esc(t.time)}</span>
    ${scarti(t, misure)}
  </div>`;

/* Le note di RFI — "CARROZZA 1 IN CODA AL TRENO - GATE B" — restano scritte
   come le scrive RFI, ma in piccolo e in grigio, con un segno davanti: prima
   erano in maiuscolo e nel colore dell'accento, e su un tabellone di Centrale
   gridavano più dei treni a cui si riferivano. */
const notaDi = (t) => (t.notes
  ? `<div class="avviso">${icona('info')}<span>${esc(t.notes)}</span></div>` : '');

/* La riga del tabellone di una stazione: dove va il treno, che treno è, il
   binario. */
function corpoStazione(t, d, misure) {
  return `${orarioDi(t, misure)}
    <div class="dove">
      <div class="destinazione">${d.arrivals ? '<span class="da">da</span> ' : ''}${esc(titolo(t.terminus))}</div>
      <span class="meta"><i class="marchio ${gruppoTreno(t)}"></i>${
        t.boarding && !t.cancelled ? '<span class="in-partenza">in partenza</span> · ' : ''}${esc(servizioDi(t))}</span>
    </div>
    ${cellaBinario(t.platform, cambioBinario(t))}
    ${notaDi(t)}`;
}

/* Dove va il treno è la destinazione scelta, uguale su ogni riga: scriverla
   quaranta volte non diceva niente. Al suo posto quello che cambia da un treno
   all'altro — a che ora arrivi e quanto ci metti.

   Il capolinea si scrive solo quando il treno va oltre, ed è l'unica volta in
   cui conta: si riconosce dalle fermate, perché la fermata scelta non è
   l'ultima. Confrontare i nomi non funzionerebbe — RFI scrive "MI.P.GARIBALDI"
   dove il catalogo dice "MILANO PORTA GARIBALDI". */
function corpoTratta(t, d, misure, piuVeloce) {
  const min = durata(t.time, t.arrival);
  const ultima = (t.stops || [])[(t.stops || []).length - 1];
  const oltre = ultima && t.arrival && ultima.time !== t.arrival;
  return `${orarioDi(t, misure)}
    <div class="dove">
      <div class="destinazione">${t.arrival
        ? `<span class="arrivo-tratta">→ ${esc(t.arrival)}</span>${min !== null
          ? `<span class="durata">${min} min${piuVeloce ? ' · il più veloce' : ''}</span>` : ''}`
        : esc(titolo(t.terminus))}</div>
      <span class="meta">${esc(servizioDi(t))}${oltre
        ? ` · <span class="oltre">prosegue per ${esc(titolo(t.terminus))}</span>` : ''}</span>
    </div>
    ${cellaBinario(t.platform, cambioBinario(t))}
    ${notaDi(t)}`;
}

/* Il prossimo treno della tratta, in grande: partenza, durata e arrivo su una
   riga, e fra quanto parte. È la risposta alla domanda per cui si apre una
   tratta salvata, e prima era la prima di quaranta righe uguali. */
function corpoPrimo(t, misure) {
  const min = durata(t.time, t.arrival);
  const fra = fraMinuti(t.time, ritardoVero(t));
  return `<div class="primo-su"><span>${esc(servizioDi(t))} · il prossimo</span>${
      fra ? `<b class="fra">fra ${fra} min</b>` : ''}</div>
    <div class="primo-viaggio">
      <span class="ora ${t.cancelled ? 'barrato' : ''}">${esc(t.time)}</span>
      <span class="freccia"><i></i>${min !== null ? `${min} min` : ''}<i></i></span>
      <span class="arrivo">${esc(t.arrival || '')}</span>
    </div>
    <div class="primo-giu">${scarti(t, misure)}${cellaBinario(t.platform, cambioBinario(t))}</div>
    ${notaDi(t)}`;
}

function rigaTreno(t, d, misure, modo = '') {
  const soppresso = t.cancelled;
  const classi = ['treno'];
  if (soppresso) classi.push('soppresso');
  else if (t.boarding) classi.push('parte');
  if (!soppresso && ritardoVero(t) > 0) classi.push('in-ritardo');
  if (modo === 'primo') classi.push('primo');

  let corpo;
  if (modo === 'primo') corpo = corpoPrimo(t, misure);
  else if (d.filtered && !d.arrivals) corpo = corpoTratta(t, d, misure, modo === 'veloce');
  else corpo = corpoStazione(t, d, misure);

  const espandibile = t.stops && t.stops.length > 0;
  if (!espandibile) {
    return `<li class="${classi.join(' ')}"><div class="riga-treno">${corpo}</div></li>`;
  }
  // La riga intera è il <summary>: toccare il treno apre le sue fermate.
  return `<li class="${classi.join(' ')}">
    <details data-treno="${esc(t.number)}"${aperti.has(t.number) ? ' open' : ''}>
      <summary class="riga-treno espandibile">${corpo}</summary>
      ${fermate(t)}
    </details>
  </li>`;
}

function fermate(t) {
  const viaggio = viaggi.get(t.number);
  const d = viaggio && viaggio.stato === 'ok' ? viaggio.dati : null;
  if (d && d.stops && d.stops.length) return viaggioReale(d) + bottoneSegui(d, t.time);

  // La fermata che interessa è quella su cui il filtro ha agganciato il treno:
  // la si riconosce dall'orario di arrivo, e va evidenziata una volta sola —
  // un treno può ripassare a orari diversi ma non due volte allo stesso.
  const evidenziata = t.arrival ? t.stops.findIndex((f) => f.time === t.arrival) : -1;
  // Disegnate sulla stessa linea del viaggio vero, che è quello che le
  // sostituisce appena arriva: con un elenco a parte la scheda si apriva in una
  // forma e un secondo dopo ne prendeva un'altra, sotto gli occhi.
  const previste = { stops: t.stops.map((f, i) => ({ name: f.name, scheduled: f.time, chosen: i === evidenziata })) };
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
  return `${elencoFermate(previste)}${nota}${d ? bottoneSegui(d, t.time) : ''}`;
}

/* "Segui" sta qui dentro, nella scheda aperta, e non sulla riga chiusa del
   tabellone.

   Il motivo è che solo qui si sa se c'è qualcosa da seguire: le coordinate con
   cui richiedere il viaggio le ha soltanto ViaggiaTreno, che su un tabellone
   non conosce tutti i treni. Un pulsante su ogni riga sarebbe morto una volta
   su due, e per scoprire quale volta bisognerebbe premerlo. Aprire la scheda,
   che è il gesto con cui si va a vedere dov'è il treno, è anche quello che
   scopre se si può seguire.

   Accanto, "Ogni giorno": lo stesso treno salvato come abitudine. Solo sulle
   partenze, perché l'ora che si salva è quella della riga, e solo lì è l'ora
   in cui si sale; sugli arrivi sarebbe l'ora in cui si scende, e il treno di
   oggi comparirebbe quando è già quasi finito. */
function bottoneSegui(d, ora) {
  if (!d.id || !d.id.origin) return '';
  // Sugli arrivi `stato.a` non è una destinazione — quel tabellone non ne ha
  // una — e passarla vorrebbe dire accendere una fermata a caso.
  const t = daSeguire(d, stato.arrivi ? null : stato.a, stato.da);
  const gia = eSeguito(t);
  // Le coordinate viaggiano nell'attributo come JSON: sono tre più
  // l'etichetta, e cinque attributi separati sarebbero cinque cose da tenere
  // allineate invece di una.
  // I giorni si propongono da lunedì a venerdì, che è il treno del lavoro:
  // chi lo prende anche il sabato lo accende in home con un tocco.
  const abituale = !stato.arrivi && stato.da && ora
    ? {
      o: t.o, n: t.n, f: stato.da, at: ora, days: [1, 2, 3, 4, 5], cat: t.cat, capolinea: t.capolinea,
      // Dove si scende resta sul telefono e non va al server: serve solo ad
      // accendere quella fermata nella scheda del treno di oggi.
      ...(t.a ? { a: t.a } : {}),
    }
    : null;
  const ogni = abituale && eAbituale(abituale);
  return `<p class="segui-riga">
    <button class="btn-testo segui" type="button" data-segui="${esc(JSON.stringify(t))}"
            aria-pressed="${gia}">${icona('segnalibro', gia)}${
      gia ? 'Lo stai seguendo' : 'Segui questo treno'}</button>
    ${abituale ? `<button class="btn-testo segui" type="button"
            data-abituale="${esc(JSON.stringify(abituale))}"
            aria-pressed="${ogni}">${icona('ripeti', ogni)}${
      ogni ? 'Lo prendi ogni giorno' : 'Ogni giorno'}</button>` : ''}
  </p>`;
}

/* Le fermate secondo ViaggiaTreno: l'ora prevista su tutte, e su quelle già
   servite il ritardo con cui il treno ci è passato.

   L'ora è sempre quella prevista, anche dove si conosce quella vera. Prima le
   fermate passate portavano l'ora reale e il ritardo accanto, e la lista si
   leggeva male: 18:40 +8 dice due volte la stessa cosa, e in mezzo alle altre
   ore — previste — non si capiva più quale colonna si stesse leggendo. Con
   l'ora prevista ferma il ritardo è l'unica cosa che cambia, e si vede.

   Non si prova a proiettare il ritardo sulle fermate future: ViaggiaTreno non
   lo fa — lì lascia zero, che è un campo non compilato e non una previsione — e
   inventarlo qui vorrebbe dire stampare un orario che nessuno ha calcolato,
   con l'aria di essere un dato. */
function viaggioReale(d) {
  // Dov'è il treno lo dice la linea delle fermate, che lo disegna fra due di
  // loro; qui resta da dire solo quando non è ancora partito.
  const nota = d.tracked ? '' : '<p class="viaggio-nota">non ancora partito</p>';
  return fasciaProvvedimento(d) + nota + elencoFermate(d);
}

function elencoFermate(d, classe, seguito = false) {
  // Il binario sta in colonna, e una colonna vuole una cella su ogni riga
  // anche dove il binario non c'è: se la si salta, l'ora di quella riga slitta
  // nella colonna del binario e la lista torna disallineata proprio dove
  // mancava il dato. La colonna esiste solo se almeno una fermata ne ha uno,
  // altrimenti sarebbe una colonna vuota lungo tutto il viaggio.
  const conBinari = d.stops.some((f) => f.platform);
  // Dove si scende. La fermata scelta quando c'è; senza — treno seguito dal
  // tabellone intero, senza una destinazione — il capolinea, che è la stessa
  // regola di fermataArrivo() per la riga del GPS: due parti della scheda
  // non devono dire due cose diverse sulla stessa fermata. Tranne se è anche
  // quella da cui si sale, che allora non è un posto dove si scende.
  // Il ripiego vale solo sulla pagina del treno seguito (`seguito`), dove la
  // riga del GPS fa già la stessa ipotesi: sulle schede del tabellone il treno
  // lo si sta solo guardando, e dirgli dove scende sarebbe inventarlo.
  const ultimaFermata = d.stops[d.stops.length - 1];
  const scesa = d.stops.find((f) => f.chosen)
    || (seguito && ultimaFermata && !ultimaFermata.boarding ? ultimaFermata : null);
  const ultima = d.stops.findLastIndex((f) => f.passed);

  const voce = (f) => {
    const classi = ['fermata'];
    if (f.passed) classi.push('passata');
    if (f.boarding) classi.push('mia');
    if (f === scesa) classi.push('mia', 'meta-scelta');
    const ora = esc(f.scheduled || f.actual || '') + (f.passed && f.delay
      ? ` <small>${segnoRitardo(f.delay)}</small>` : '');
    // "Sali qui" e "Scendi qui" per nome e non solo per colore: il colore
    // diceva che quella fermata era diversa, non perché.
    const cosa = f === scesa ? 'Scendi qui' : (f.boarding ? 'Sali qui' : '');
    return `<li class="${classi.join(' ')}"><span class="punto"></span>
      <span class="nome">${cosa ? `<small>${cosa}</small>` : ''}${esc(titolo(f.name))}</span>
      <time>${ora}</time>${conBinari ? binarioFermata(f) : ''}</li>`;
  };

  /* Le fermate servite prima dell'ultima si ripiegano: a viaggio inoltrato
     erano dieci righe spente prima di arrivare a quella che si cercava, cioè
     dov'è il treno adesso. L'ultima servita resta fuori, perché è da lì che
     il treno è appena passato. */
  const prima = ultima > 0 ? d.stops.slice(0, ultima) : [];
  const chiave = d.id ? `${d.id.origin}|${d.id.number}|${d.id.date}` : '';
  // Le due liste sono un viaggio solo spezzato in due, e devono avere le
  // stesse colonne: con quattro celle in una griglia da tre il binario delle
  // fermate ripiegate andava a capo, sopra il filo verticale.
  const classi = ['fermate', 'linea'];
  if (conBinari) classi.push('con-binari');
  if (classe) classi.push(classe);
  const ripiegate = prima.length ? `<details class="precedenti" data-precedenti="${esc(chiave)}"${
    precedentiAperte.has(chiave) ? ' open' : ''}>
      <summary>${icona('giu')}${prima.length} ${prima.length === 1 ? 'fermata precedente' : 'fermate precedenti'}</summary>
      <ol class="${classi.join(' ')}">${prima.map(voce).join('')}</ol>
    </details>` : '';

  const voci = d.stops.slice(prima.length).map((f, i) =>
    voce(f) + (prima.length + i === ultima ? trenoFra(d, ultima) : '')).join('');
  return `${ripiegate}<ol class="${classi.join(' ')}">${voci}</ol>`;
}

/* Il treno, disegnato fra l'ultima fermata servita e la prossima.

   Non è l'ultimo rilevamento di ViaggiaTreno, che spesso non è una stazione
   — "1°BIVIO FIDENZA OVEST" — ma le fermate stesse: quelle che ViaggiaTreno
   dà per servite, e la prima che non lo è ancora. Fra un rilevamento e l'altro
   il treno può aver già passato la prossima, quindi vale "all'ultimo
   rilevamento", come tutto il resto della scheda.

   A che ora arriverà alla prossima non si scrive: sarebbe l'ora prevista più
   il ritardo di adesso, cioè una proiezione che nessuno ha calcolato, e l'ora
   prevista c'è già sulla riga sotto. */
function trenoFra(d, ultima) {
  const da = d.stops[ultima];
  const a = d.stops[ultima + 1];
  if (!d.tracked || d.arrived || !da || !a) return '';
  const quando = [da.actual ? `alle ${esc(da.actual)}` : '', da.delay ? segnoRitardo(da.delay) : '']
    .filter(Boolean).join(', ');
  return `<li class="treno-qui"><span class="icona-treno">${icona('treno')}</span>
    <p><b>Fra ${esc(titolo(da.name))} e ${esc(titolo(a.name))}</b>
      <small>passato da ${esc(titolo(da.name))}${quando ? ` ${quando}` : ''}</small></p></li>`;
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

/* Un viaggio che non ha niente da mostrare: il treno che ViaggiaTreno non
   conosce, la lettura andata male, la richiesta ancora in volo. Distinguerlo da
   un viaggio vero serve due volte — per sapere quali vale la pena richiedere, e
   per non lasciare che una risposta vuota ne cancelli uno che si aveva già. */
const viaggioVuoto = (v) => !(v && v.stato === 'ok'
  && v.dati && v.dati.stops && v.dati.stops.length);

/* Scarica il viaggio di un treno.

   Parte quando qualcuno apre la scheda, e si rifà a ogni rinfresco del
   tabellone finché la scheda resta aperta: non per tutti e quaranta i treni,
   che sarebbero quaranta richieste a un servizio lento per le due o tre schede
   che si aprono davvero.

   Rileggerlo non è un lusso. La scheda risponde a "dov'è il treno adesso", e
   una lettura di mezz'ora fa non lo dice più; ma soprattutto è il solo momento
   in cui un "ViaggiaTreno non segue questo treno" può correggersi. Quella
   risposta è vera per il treno che parte fra tre ore — ViaggiaTreno pubblica la
   stazione un'ora e mezza per volta, il tabellone di RFI arriva a notte — ed
   era vera anche per il mezzo minuto in cui il suo servizio non aveva
   risposto. Chiesta una volta sola, restava lì per sempre: sulla riga sopra
   compariva la pastiglia verde col ritardo misurato sul treno, e la scheda
   sotto continuava a dire che quel treno nessuno lo segue. */
async function scaricaViaggio(numero) {
  const gia = viaggi.get(numero);
  // Una richiesta in volo non si raddoppia: il rinfresco arriva ogni minuto e
  // ViaggiaTreno ogni tanto se ne prende otto secondi.
  if (gia && gia.stato === 'attesa') return;
  // L'attesa si mostra solo la prima volta. Su una rilettura resta in pagina il
  // viaggio di prima: sostituirlo con "cerco dov'è il treno…" a ogni minuto
  // farebbe sfarfallare una scheda che si sta leggendo.
  if (!gia) {
    viaggi.set(numero, { stato: 'attesa' });
    disegna();
  }
  // Da dove si è chiesto. Se nel frattempo si è cambiato tabellone, questa
  // risposta riguarda dei treni che non ci sono più: `viaggi` è già stato
  // svuotato per il tabellone nuovo, e riempirlo qui vorrebbe dire rimetterci
  // dentro il viaggio del tabellone di prima.
  const dove = chiaveTabellone();
  try {
    const d = await leggiJSON(API.treno(stato.da, numero, stato.a, stato.arrivi));
    if (!d) return;   // la pagina si sta ricaricando
    if (dove !== chiaveTabellone()) return;
    // Una risposta vuota non cancella un viaggio che si aveva già: vale la
    // stessa regola dell'errore qui sotto, ed è il caso del treno che
    // ViaggiaTreno perde di vista per una lettura.
    if (viaggioVuoto({ stato: 'ok', dati: d }) && !viaggioVuoto(gia)) return;
    viaggi.set(numero, { stato: 'ok', dati: d });
  } catch {
    // Un viaggio che non arriva non è un guasto della pagina: restano le
    // fermate previste, che è quello che si vedeva prima di questa aggiunta —
    // o il viaggio della lettura precedente, se ce n'era uno, perché un
    // servizio che non risponde adesso non è un treno che non si può seguire.
    if (dove !== chiaveTabellone() || !viaggioVuoto(gia)) return;
    viaggi.set(numero, { stato: 'errore' });
  }
  disegna();
}

/* ---- tira per aggiornare ---- */

/* Rilegge quello che la vista corrente rilegge già una volta al minuto: è lo
   stesso giro, chiesto con un gesto invece che dal timer. Torna una promessa
   perché il cerchio deve girare finché i dati non sono arrivati. */
function rinfrescaVista() {
  const r = leggiRotta();
  if (r.vista === 'home') return rinfrescaHome();
  if (r.vista === 'risultati') return caricaTabellone();
  if (r.vista === 'linee') return caricaLinee().then(() => { if (leggiRotta().vista === 'linee') disegna(); });
  if (r.vista === 'treno') return rilettura(r.treno)();
  return Promise.resolve();
}

/* Trascinare in giù con la pagina in cima aggiorna, come in ogni app.

   Il browser non lo fa da sé: Safari no, e Chrome lo spegne perché il body ha
   overscroll-behavior-y: contain, messo per non far rimbalzare la pagina. Sono
   tre ascoltatori sul documento e un cerchio che segue il dito; nessuna
   libreria, perché il gesto è questo e basta.

   Parte solo con scrollY a zero: a metà di una lista lunga trascinare in giù è
   scorrere. E non dentro la mappa, che il dito lo usa per spostarla, né nel
   selettore stazione, che è una lista sua.

   Il cerchio non scatta mai: col dito sopra lo segue alla lettera (la classe
   segue spegne le transizioni, se no lo rincorrerebbe in ritardo), lasciato
   scivola dove deve andare. La posizione sta in translate e il giro del dito in
   rotate, non nel transform: il transform è dell'animazione gira, che così
   continua dall'angolo in cui il dito l'ha lasciato invece di ripartire da zero. */
function tiraPerAggiornare(el, soglia, rinfresca) {
  let inizio = null;   // la Y del tocco, o null se il gesto non è partito
  let tirato = 0;
  let occupato = false;
  document.addEventListener('touchstart', (e) => {
    const t = e.target;
    const dentro = t && typeof t.closest === 'function' && t.closest('.mappa, #scelta');
    // Minore o uguale: durante il rimbalzo iOS dà scrollY negativo.
    inizio = !occupato && scrollY <= 0 && !dentro ? e.touches[0].clientY : null;
    tirato = 0;
    if (inizio !== null) el.classList.add('segue');
  }, { passive: true });
  document.addEventListener('touchmove', (e) => {
    if (inizio === null) return;
    const dy = e.touches[0].clientY - inizio;
    // Il dito che sale è la pagina che scorre in giù dalla cima: il gesto non
    // c'entra più. Il cerchio sta sopra l'intestazione (z-index 6 contro 5), e
    // senza questo comparirebbe lì a ogni scorrimento partito da scrollY 0.
    if (dy < 0) { inizio = null; lascia(); return; }
    // Il cerchio segue il dito con un freno: a metà strada si sente che manca
    // poco, oltre la soglia che è fatta. Sotto i 4 px resta nascosto, perché un
    // tocco che trema non lo faccia lampeggiare. Intanto gira col tirato, anche
    // oltre il tetto: il dito che va ancora avanti si vede.
    tirato = dy * 0.5;
    el.style.translate = `-50% ${Math.min(tirato, soglia * 1.3)}px`;
    el.style.rotate = `${tirato * 3}deg`;
    el.classList.toggle('pronto', tirato >= soglia);
    el.classList.toggle('visibile', tirato > 4);
  }, { passive: true });
  // Tolto segue tornano le transizioni: il cerchio risale svanendo e si srotola.
  const lascia = () => {
    el.classList.remove('visibile', 'pronto', 'segue');
    el.style.translate = '';
    el.style.rotate = '';
  };
  // Un tocco annullato dal sistema (una chiamata, un gesto di iOS) è un
  // rilascio sotto soglia: il cerchio se ne va e non si rilegge niente.
  document.addEventListener('touchcancel', () => {
    if (inizio === null) return;
    inizio = null;
    lascia();
  }, { passive: true });
  document.addEventListener('touchend', () => {
    if (inizio === null) return;
    inizio = null;
    if (tirato < soglia) { lascia(); return; }
    occupato = true;
    // Da dov'era scivola alla soglia e lì gira, partendo dall'angolo che ha.
    el.classList.remove('segue');
    el.classList.add('gira');
    el.style.translate = `-50% ${soglia}px`;
    Promise.resolve(rinfresca()).catch(() => {}).then(() => {
      // Prima svanisce, poi torna su e smette di girare: tutto insieme, il
      // cerchio salterebbe in cima mentre ancora si vede. I 150 ms sono la
      // dissolvenza di .tira in app.css. Il gesto resta occupato fino ad allora,
      // perché un tocco nuovo non si veda togliere il cerchio da sotto il dito.
      el.classList.remove('visibile');
      setTimeout(() => {
        occupato = false;
        el.classList.remove('pronto', 'gira');
        el.style.translate = '';
        el.style.rotate = '';
      }, 150);
    });
  }, { passive: true });
}

/* ------------------------------------------------------------------ eventi */

tiraPerAggiornare($('#tira'), 70, rinfrescaVista);

/* Il segnalibro si tocca da due posti — la scheda aperta di un tabellone e la
   scheda del treno stesso — e fa la stessa cosa da tutti e due. */
function alternaSeguitoDa(el) {
  const t = JSON.parse(el.dataset.segui);
  alternaSeguito(t);
  const seguo = eSeguito(t);
  // Il permesso si chiede qui e non dentro sincronizzaNotifiche: su iOS vale
  // solo se la chiamata parte durante il tocco, e dopo un await il tocco non
  // c'è più. È la stessa cosa che fa la campanella di una linea.
  //
  // Seguire un treno è il gesto che dice "avvisami": chiederlo qui evita che
  // chi non ha mai acceso una campanella metta il segnalibro e non riceva mai
  // niente senza capire perché.
  const permesso = permessoSe(seguo);
  // Appena seguito, il viaggio si scarica subito: tornando in home la scheda
  // dev'essere già piena, non ancora in attesa. Il server lo tiene in cache
  // trenta secondi, quindi è la stessa lettura appena fatta.
  if (seguo) caricaViaggioSeguito(t).then(disegna);
  disegna();
  sincronizzaNotifiche(permesso);
}

/* "Ogni giorno" è lo stesso gesto, con un'abitudine al posto del segnalibro:
   il permesso si chiede dentro il tocco per la stessa ragione di iOS. Niente
   viaggio da scaricare — il treno di oggi compare in home quando è ora. */
function alternaAbitualeDa(el) {
  const x = JSON.parse(el.dataset.abituale);
  alternaAbituale(x);
  const permesso = permessoSe(eAbituale(x));
  disegna();
  sincronizzaNotifiche(permesso);
}

/* In home, ogni tocco su un abituale salva e riallinea il server, come le
   fasce: un giorno spento e non mandato sarebbe una notifica che arriva lo
   stesso. */
function cambiaAbituale(k, muta) {
  scrivi('tt.abituali', abituali().flatMap((x) => (chiaveAbituale(x) === k ? muta(x) : [x])));
  disegna();
  sincronizzaNotifiche();
}

app.addEventListener('click', (e) => {
  const t = e.target;
  if (t.closest('[data-ricentra]')) {
    if (mappa) { mappa.seguiMe = true; aggiornaPosizione(); }
    return;
  }
  if (t.closest('[data-mappa]')) {
    mappaAperta = !mappaAperta;
    // Aprendola si accende anche il GPS, che è quello che la mappa ha da
    // dire: dentro il tocco, perché su iOS dopo un await il gesto non c'è più
    // e il permesso non viene chiesto. Chiudendola non si spegne — la riga
    // qui sopra continua a misurare quanto manca alla fermata.
    if (mappaAperta) {
      scrivi('tt.posizione', true);
      avviaPosizione();
    }
    aggiornaVista();
    return;
  }
  if (t.closest('[data-gps]')) {
    // Dentro il tocco, come per la campanella: su iOS dopo un await il gesto
    // non c'è più e il permesso non viene chiesto.
    scrivi('tt.posizione', true);
    avviaPosizione();
    aggiornaVista();
    return;
  }
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
    cambiaFasce((f) => { f[i].giorni = alternaGiorno(f[i].giorni, g); });
  }
  else if (t.closest('[data-segui]')) alternaSeguitoDa(t.closest('[data-segui]'));
  else if (t.closest('[data-abituale]')) alternaAbitualeDa(t.closest('[data-abituale]'));
  else if (t.closest('[data-togli-abituale]')) {
    // Si chiede prima: un abituale tolto per sbaglio si rimette solo
    // ritrovando il treno sul tabellone, cioè domani alla stessa ora.
    const k = t.closest('[data-togli-abituale]').dataset.togliAbituale;
    const x = abituali().find((y) => chiaveAbituale(y) === k);
    if (x && !confirm(`Togliere il ${[x.cat, x.n].filter(Boolean).join(' ')} delle ${x.at} dai treni abituali?\n` +
      'Per rimetterlo dovrai ritrovarlo sul tabellone.')) return;
    cambiaAbituale(k, () => []);
  }
  else if (t.closest('[data-giorno-abituale]')) {
    const v = t.closest('[data-giorno-abituale]').dataset.giornoAbituale;
    const i = v.lastIndexOf(':');
    const g = Number(v.slice(i + 1));
    cambiaAbituale(v.slice(0, i), (x) => [{ ...x, days: alternaGiorno(x.days, g) }]);
  }
  else if (t.closest('[data-apri-abituale]')) {
    const k = t.closest('[data-apri-abituale]').dataset.apriAbituale;
    if (!abitualiAperti.delete(k)) abitualiAperti.add(k);
    disegna();
  }
  else if (t.closest('[data-gettone]')) {
    if (t.closest('[data-gettone]').dataset.gettone === 'scioperi') scioperiAperti = !scioperiAperti;
    else avvisiStazioneAperti = !avvisiStazioneAperti;
    disegna();
  }
  else if (t.closest('[data-modo]')) { modoRicerca = t.closest('[data-modo]').dataset.modo; disegna(); }
  else if (t.closest('[data-tipo]')) { filtroTipo = t.closest('[data-tipo]').dataset.tipo; disegna(); }
  else if (t.closest('[data-apri]')) apriScelta(t.closest('[data-apri]').dataset.apri);
  else if (t.closest('[data-vai]')) vaiAiRisultati();
  else if (t.closest('[data-scambia]')) {
    [stato.da, stato.a] = [stato.a, stato.da];
    scriviCampi();
    disegna();
  }
  else if (t.closest('[data-modifica]')) { modificaPreferiti = !modificaPreferiti; disegna(); }
  else if (t.closest('[data-campanella]')) {
    const codice = t.closest('[data-campanella]').dataset.campanella;
    const accendo = !seguita(codice);
    alternaCampanella(codice);
    // Il permesso si chiede qui e non dentro sincronizzaNotifiche: su iOS
    // vale solo se la chiamata parte durante il tocco, e dopo un await il
    // tocco non c'è più. La promessa la si aspetta di là.
    const permesso = permessoSe(accendo);
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

/* `toggle` non fa bubbling: si ascolta in fase di cattura sul contenitore.

   E non arriva solo da un dito. Ogni ridisegno rifà il contenitore da capo, e
   un <details> che nasce già aperto — perché lo si era aperto prima, e il
   disegno lo rispetta — annuncia un toggle come se qualcuno l'avesse appena
   toccato. Un toggle che dice quello che si sapeva già non è un gesto: è la
   pagina che si rilegge addosso quello che aveva scritto lei, e va ignorato.

   Senza questo controllo la scheda di un treno aperta sul tabellone mandava la
   pagina in tondo: il toggle rileggeva il viaggio, la risposta ridisegnava, il
   ridisegno rifaceva il <details> aperto e ne usciva un altro toggle, cioè un
   altro viaggio da rileggere — centinaia di richieste al minuto, e la pagina
   ricostruita sotto le dita così in fretta che nessun tocco arrivava più a
   diventare un click: né il tasto per tornare indietro, né "Segui questo
   treno". Lo stesso giro lo faceva una linea la cui lettura degli avvisi era
   fallita, che si rifà a ogni apertura. */
app.addEventListener('toggle', (e) => {
  const d = e.target;
  if (!(d instanceof HTMLDetailsElement)) return;
  // Le fermate ripiegate e i gruppi di linee non chiedono niente al server:
  // basta ricordarsi com'erano, per il prossimo ridisegno.
  if ('precedenti' in d.dataset) {
    if (d.open) precedentiAperte.add(d.dataset.precedenti); else precedentiAperte.delete(d.dataset.precedenti);
    return;
  }
  if ('gruppo' in d.dataset) {
    if (d.open) gruppiLineeAperti.add(d.dataset.gruppo); else gruppiLineeAperti.delete(d.dataset.gruppo);
    return;
  }
  if (d.dataset.linea) {
    const codice = d.dataset.linea;
    if (d.open === lineeAperte.has(codice)) return;
    if (d.open) { lineeAperte.add(codice); scaricaAvvisi(codice); }
    else lineeAperte.delete(codice);
    return;
  }
  if (!d.dataset.treno) return;
  const numero = d.dataset.treno;
  if (d.open === aperti.has(numero)) return;
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

/* Niente zoom. La pagina è disegnata per la larghezza del telefono, e nello
   zoom ci si finiva per sbaglio: il doppio tocco su una riga per aprirla, le
   due dita mentre si trascina la mappa. La regola sta nel viewport e nel
   foglio di stile; Safari su iOS ignora il primo fuori dall'app installata e
   ha imparato tardi il secondo, quindi il gesto delle due dita si ferma anche
   qui. Sono eventi solo di WebKit: altrove non arrivano e non fanno niente. */
for (const gesto of ['gesturestart', 'gesturechange']) {
  document.addEventListener(gesto, (e) => e.preventDefault(), { passive: false });
}

window.addEventListener('hashchange', cambiaRotta);
// Prima della prima rotta: la home deve poter disegnare le schede seguite con
// l'ultima lettura salvata, senza aspettare una rete che magari non c'è.
idrataViaggi();
idrataCampi();
cambiaRotta();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('sw.js').catch(() => {});
    // Riallinea l'abbonamento a ogni avvio: il servizio potrebbe averlo perso,
    // e chi ha una campanella accesa o un treno da aspettare non deve
    // accorgersene.
    if (campanelle().length || seguiti().length || abituali().length) sincronizzaNotifiche();
  });
}
