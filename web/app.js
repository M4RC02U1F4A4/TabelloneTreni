'use strict';

/* Interfaccia dei tabelloni. Niente framework e niente passo di build: tutto
   quello che serve è una lista che si ridisegna una volta al minuto, e il
   costo di scaricare una libreria per farlo si pagherebbe a ogni apertura. */

const API = {
  stazioni: 'api/stations',
  tabellone: (da, a, arrivi) =>
    `api/board?from=${da}` + (a ? `&to=${a}` : '') + (arrivi ? '&arrivals=true' : ''),
};

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
};

// Con "Modifica" attivo le righe dei preferiti mostrano la ✕. Fuori da quella
// modalità non c'è: una ✕ accanto a una riga tappabile mette la cancellazione a
// un dito dal gesto che si fa ogni giorno.
let modificaPreferiti = false;
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
  const evidenzia = (nome) => {
    if (!query) return esc(nome);
    const i = canon(nome).indexOf(query);
    if (i < 0) return esc(nome);
    return esc(nome.slice(0, i)) + '<mark>' + esc(nome.slice(i, i + query.length)) +
           '</mark>' + esc(nome.slice(i + query.length));
  };
  listaScelta.innerHTML =
    (intestazione ? `<li class="intestazione">${esc(intestazione)}</li>` : '') +
    voci.map(([id, nome]) =>
      `<li><button type="button" data-id="${id}">${evidenzia(nome)}</button></li>`).join('');
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
  if (parti[0] !== 'p' && parti[0] !== 'a') return { vista: 'home' };
  const da = Number(parti[1]);
  if (!da) return { vista: 'home' };
  return { vista: 'risultati', da, a: Number(parti[2]) || null, arrivi: parti[0] === 'a' };
}

const rottaDi = (da, a, arrivi) => `#/${arrivi ? 'a' : 'p'}/${da}` + (a && !arrivi ? `/${a}` : '');

function vaiAiRisultati() {
  if (!stato.da) return;
  location.hash = rottaDi(stato.da, stato.a, stato.arrivi);
}

async function cambiaRotta() {
  const r = leggiRotta();
  fermaTimer();

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
    return;
  }

  stato.da = r.da; stato.a = r.a; stato.arrivi = r.arrivi;
  stato.dati = null;
  stato.errore = null;
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
  if (document.visibilityState !== 'visible' || leggiRotta().vista !== 'risultati') return;
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
  if (r.vista === 'home') disegnaHome(); else disegnaRisultati();
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
    </section>`;
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
    <details>
      <summary class="${riga}">${contenuto}</summary>
      ${fermate(t)}
    </details>
  </li>`;
}

function fermate(t) {
  // La fermata che interessa è quella su cui il filtro ha agganciato il treno:
  // la si riconosce dall'orario di arrivo, e va evidenziata una volta sola —
  // un treno può ripassare a orari diversi ma non due volte allo stesso.
  const evidenziata = t.arrival ? t.stops.findIndex((f) => f.time === t.arrival) : -1;
  const voci = t.stops.map((f, i) =>
    `<li class="${i === evidenziata ? 'meta-scelta' : ''}">
      <span>${esc(f.name)}</span><time>${esc(f.time)}</time></li>`).join('');
  return `<ol class="fermate">${voci}</ol>`;
}

/* ------------------------------------------------------------------ eventi */

app.addEventListener('click', (e) => {
  const t = e.target;
  if (t.closest('[data-apri]')) apriScelta(t.closest('[data-apri]').dataset.apri);
  else if (t.closest('[data-vai]')) vaiAiRisultati();
  else if (t.closest('[data-scambia]')) { [stato.da, stato.a] = [stato.a, stato.da]; disegna(); }
  else if (t.closest('[data-modifica]')) { modificaPreferiti = !modificaPreferiti; disegna(); }
  else if (t.closest('[data-togli]')) {
    const k = t.closest('[data-togli]').dataset.togli;
    scrivi('tt.preferiti', preferiti().filter((p) => chiaveTratta(p) !== k));
    disegna();
  }
});

testa.addEventListener('click', (e) => {
  if (!e.target.closest('[data-preferito]')) return;
  alternaPreferito({ f: stato.da, t: stato.arrivi ? null : stato.a, a: stato.arrivi || undefined });
  disegnaRisultati();
});

window.addEventListener('hashchange', cambiaRotta);
cambiaRotta();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => navigator.serviceWorker.register('sw.js').catch(() => {}));
}
