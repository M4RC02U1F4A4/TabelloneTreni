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
  // Un tabellone arrivi ha bisogno di una stazione sola, quindi non gli serve
  // un modulo: scelta la stazione, ci si va direttamente.
  if (campoInModifica === 'arrivi') {
    chiudiScelta();
    location.hash = `#/a/${id}`;
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
                aria-label="Inverti partenza e arrivo">⇅</button>
      </div>
      <button class="principale" type="button" data-vai ${stato.da ? '' : 'disabled'}>
        Vedi i treni
      </button>
    </section>

    <section class="sezione">
      <ul class="lista">
        <li class="riga">
          <button class="riga-tocco" type="button" data-apri="arrivi">
            <span class="segno tenue">↓</span>
            <span class="testo">Arrivi di una stazione</span>
            <span class="chevron">›</span>
          </button>
        </li>
      </ul>
    </section>`;
}

function campoStazione(quale, sigla, id, vuoto) {
  return `<button class="campo" type="button" data-apri="${quale}">
    <span class="sigla">${sigla}</span>
    <span class="valore ${id ? '' : 'vuoto'}">${id ? esc(nomeStazione(id)) : vuoto}</span>
    <span class="chevron">›</span>
  </button>`;
}

function rigaPreferito(p) {
  return `<li class="riga">
    <a class="riga-tocco" href="${rottaDi(p.f, p.t, p.a)}">
      <span class="segno">★</span>
      <span class="testo">${etichettaPreferito(p)}</span>
      ${modificaPreferiti ? '' : '<span class="chevron">›</span>'}
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
      <a class="tasto" href="#/" aria-label="Torna alla home">←</a>
      <h1 class="titolo">${esc(daNome)}${aNome && !stato.arrivi ?
        ` <span class="freccia">→</span> ${esc(aNome)}` : ''}</h1>
      <button class="tasto" type="button" data-preferito aria-pressed="${salvato}"
              aria-label="${salvato ? 'Togli dai preferiti' : 'Aggiungi ai preferiti'}">${salvato ? '★' : '☆'}</button>
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
      : '<div class="scheletro"></div>'.repeat(4);
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
    ? `<ul>${d.trains.map((t) => rigaTreno(t, d)).join('')}</ul>`
    : `<div class="senza-risultati">
         <p>Nessun treno da ${esc(daNome)}${d.filtered ? ` che ferma a ${esc(aNome)}` : ''}.</p>
         <p>Il tabellone copre solo le prossime ore.</p>
       </div>`;

  app.innerHTML = note.join('') + corpo;
}

function rigaTreno(t, d) {
  const soppresso = t.cancelled;
  const classi = ['treno'];
  if (soppresso) classi.push('soppresso');
  else if (t.boarding) classi.push('parte');

  let scarto = '';
  if (soppresso) scarto = '<span class="scarto">soppresso</span>';
  else if (t.delay > 0) scarto = `<span class="scarto">+${t.delay}′</span>`;
  else if (t.status) scarto = `<span class="scarto">${esc(t.status.toLowerCase())}</span>`;

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
    <div class="binario">
      ${t.platform ? `<span class="num">${esc(t.platform)}</span><span class="cap">BIN</span>`
                   : '<span class="ignoto" title="Binario non ancora assegnato">–</span>'}
    </div>
    ${espandibile ? '<span class="apri" aria-hidden="true">›</span>' : ''}
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
