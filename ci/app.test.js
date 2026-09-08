/* I controlli sul frontend: `node ci/app.test.js`.
 *
 * app.js è scritto per il browser e non esporta niente: qui se ne legge il
 * sorgente e si valutano i pezzi che servono, così i controlli girano sul
 * codice vero invece che su una copia destinata a divergere.
 *
 * Sta in ci/ e non accanto ad app.js perché main.go fa `//go:embed all:web`:
 * là dentro finirebbe nel binario, e verrebbe servito come una pagina. */
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.js'), 'utf8');

// Ritaglia dal sorgente il pezzo fra due ancore, e fallisce se non lo trova:
// se qualcuno sposta o rinomina, il controllo lo dice invece di sparire.
function ritaglia(da, a) {
  const i = src.indexOf(da);
  const j = src.indexOf(a);
  assert.ok(i >= 0 && j > i, `ancore non trovate in app.js: ${da} … ${a}`);
  return src.slice(i, j);
}

/* ---------------------------------------------------- titolo() sui nomi RFI */

const titolo = new Function(`${ritaglia('const MINORI', '/* Segna nel nome')}; return titolo;`)();

for (const [dato, atteso] of [
  ['MILANO PORTA GARIBALDI', 'Milano Porta Garibaldi'],
  ['LA SPEZIA CENTRALE', 'La Spezia Centrale'],
  ['ALBA ADRIATICA-NERETO-CONTROGUERRA', 'Alba Adriatica-Nereto-Controguerra'],
  ['S.MARIA LA LONGA', 'S.Maria la Longa'],
  ['ANZOLA DELL\'EMILIA', 'Anzola dell\'Emilia'],
  ['BORGHETTO SULL\'ADIGE', 'Borghetto sull\'Adige'],
  ['BIVIO D\'AURISINA', 'Bivio d\'Aurisina'],
  ['STAZIONE PER L\'ALPAGO', 'Stazione per l\'Alpago'],
  ['MILANO P.TA GARIBALDI', 'Milano P.ta Garibaldi'],   // troncamento: minuscolo
  ['BOLOGNA C.LE/AV', 'Bologna C.le/AV'],
  ['ACQUEDOLCI-S.FRATELLO', 'Acquedolci-S.Fratello'],   // iniziale puntata: no
  ['COLLEVALENZA R.R.', 'Collevalenza R.R.'],
  ['PM S.LEONARDO DI CUTRO', 'PM S.Leonardo di Cutro'], // sigla di esercizio
  ['NAPOLI AFRAGOLA PES', 'Napoli Afragola PES'],
  ['CUZZAGO (MI)', 'Cuzzago (MI)'],                     // sigla di provincia
  ['COSTIGLIOLE (MOTTA DI)', 'Costigliole (Motta di)'],
  ['CANICATTI\'', 'Canicatti\''],                       // accento non indovinato
  ['CASSANO D`ADDA', 'Cassano d`Adda'],                 // l'apostrofo storto di RFI
]) {
  assert.strictEqual(titolo(dato), atteso, dato);
}

// Sul catalogo intero: la lunghezza non cambia mai, e le uniche cose che
// restano urlate sono le sigle dichiarate.
const catalogo = path.join(__dirname, '..', 'internal', 'stations', 'stations.json');
const nomi = JSON.parse(fs.readFileSync(catalogo, 'utf8')).stations.map((s) => s.n);
assert.ok(nomi.length > 2000, 'catalogo troppo corto, il file è cambiato?');

for (const n of nomi) {
  assert.strictEqual(titolo(n).length, n.length, `lunghezza cambiata su ${n}`);
  for (const t of titolo(n).match(/\b[A-Z]{2,}\b/g) || []) {
    assert.ok(['AV', 'MI', 'NO', 'PC', 'PES', 'PM'].includes(t), `${n} → resta urlato: ${t}`);
  }
}

console.log(`titolo: ok — 18 casi + ${nomi.length} stazioni del catalogo`);

/* ------------------------------------- la tratta aperta per ultima, in home */

/* preferitiOrdinati() riconosce il preferito da guardare confrontando la
 * chiave della tratta con quella salvata quando si apre un tabellone. Le due
 * chiavi nascono da due forme diverse — un preferito salvato e una rotta letta
 * dall'URL — e se smettessero di combaciare non si romperebbe niente a schermo:
 * la home resterebbe semplicemente nell'ordine di prima, in silenzio. */
const pezzoChiavi = ritaglia('const chiaveTratta =', 'function alternaPreferito');
const chiaveTratta = new Function(`${pezzoChiavi}; return chiaveTratta;`)();
const chiaveRotta = new Function(`${pezzoChiavi}; return chiaveRotta;`)();
const rottaDi = new Function(`${ritaglia('const rottaDi =', 'const ROTTA_LINEE')}; return rottaDi;`)();

// leggiRotta() legge location.hash: le si dà un location finto e per il resto
// gira il codice vero.
const leggiRotta = new Function('location',
  `${ritaglia('function leggiRotta()', 'const rottaDi =')}; return leggiRotta;`);

for (const preferito of [
  { f: 1393, t: 1715, a: undefined },        // Gallarate → Milano P. Garibaldi
  { f: 1715, t: 1393, a: undefined },        // il ritorno
  { f: 1393, t: null, a: undefined },        // tutte le partenze da Gallarate
  { f: 1393, t: null, a: true },             // gli arrivi a Gallarate
]) {
  // Il giro completo: dal preferito all'indirizzo su cui porta, da lì alla
  // rotta che il router legge, e da quella alla chiave che si salva.
  const r = leggiRotta({ hash: rottaDi(preferito.f, preferito.t, preferito.a) })();
  assert.strictEqual(r.vista, 'risultati', JSON.stringify(preferito));
  assert.strictEqual(
    chiaveRotta(r),
    chiaveTratta(preferito),
    `la chiave della rotta non combacia con quella del preferito: ${JSON.stringify(preferito)}`);
}

// Due tratte diverse non devono cadere sulla stessa chiave, altrimenti in cima
// finirebbe quella sbagliata.
const chiavi = [
  { f: 1393, t: 1715 }, { f: 1715, t: 1393 }, { f: 1393, t: null },
  { f: 1393, t: null, a: true }, { f: 1715, t: null },
].map(chiaveTratta);
assert.strictEqual(new Set(chiavi).size, chiavi.length, `chiavi in collisione: ${chiavi}`);

/* E ricordaTratta() se ne ricorda solo quando è una tratta salvata: una
 * ricerca al volo non deve cancellare quale preferito si stava usando. */
const salvate = [{ f: 1393, t: 1715 }, { f: 1715, t: 1393 }];
const nuovaRicorda = (rotta) => {
  let scritto;
  new Function('preferiti', 'scrivi',
    `${pezzoChiavi}; return ricordaTratta;`)(() => salvate, (_k, v) => { scritto = v; })(rotta);
  return scritto;
};

assert.strictEqual(
  nuovaRicorda({ da: 1715, a: 1393, arrivi: false }), '1715>1393',
  'una tratta salvata deve essere ricordata');
assert.strictEqual(
  nuovaRicorda({ da: 2263, a: null, arrivi: false }), undefined,
  'una ricerca al volo non deve toccare la tratta ricordata');
assert.strictEqual(
  nuovaRicorda({ da: 1393, a: null, arrivi: true }), undefined,
  'gli arrivi a una stazione salvata solo in partenza non sono quella tratta');

console.log('tratte: ok — 4 giri completi + 3 casi su ricordaTratta');

/* ------------------------------------------- il numero del binario e il SOT */

/* numeroBinario() è l'unico punto dell'app che compone HTML a pezzi invece di
 * passare tutto da esc(), quindi il controllo guarda due cose: che stacchi la
 * qualifica solo quando c'è davvero, e che quello che RFI ci mette dentro non
 * esca mai dal template. */
const esc = new Function(`${ritaglia('const esc =', '/* RFI manda i nomi')}; return esc;`)();
const numeroBinario = new Function('esc',
  `${ritaglia('function numeroBinario', '/* Il provvedimento')}; return numeroBinario;`)(esc);

for (const [dato, atteso] of [
  ['2', '2'],
  ['10', '10'],
  ['2 SOT', '2<small>SOT</small>'],        // i sotterranei di Porta Garibaldi
  ['1 SOT', '1<small>SOT</small>'],
  ['EST', 'EST'],                          // niente cifra davanti: tale e quale
  ['', ''],
]) {
  assert.strictEqual(numeroBinario(dato), atteso, dato);
}

// RFI scrive a mano in quella casella: qualunque cosa ci finisca esce escapata.
assert.strictEqual(numeroBinario('<b>x'), '&lt;b&gt;x');
assert.strictEqual(numeroBinario('3 <b>x'), '3<small>&lt;b&gt;x</small>');

// Sotto la cifra non deve più comparire "bin.", ma la didascalia deve tornare
// quando il binario cambia — e la parola resta per chi ascolta la pagina.
const cellaBinario = new Function('esc',
  `${ritaglia('function numeroBinario', '/* Il provvedimento')}; return cellaBinario;`)(esc);

const normale = cellaBinario('2', null);
assert.ok(!/bin\./.test(normale), 'la didascalia "bin." non va più scritta');
assert.ok(/solo-lettori">binario</.test(normale), 'la parola resta per lo screen reader');
assert.ok(!/class="cap"/.test(normale), 'senza cambio non c\'è didascalia visibile');

const cambiato = cellaBinario('5', 'era 3');
assert.ok(/class="cap">era 3</.test(cambiato), 'a binario cambiato la didascalia torna');
assert.ok(/binario cambiato"/.test(cambiato), 'e la cella si marca come cambiata');

assert.ok(/ignoto/.test(cellaBinario('', null)), 'senza binario resta il trattino');

console.log('binario: ok — 6 casi + 2 di escaping + 6 sulla cella');

/* ------------------------------------ la barretta che conta il minuto */

/* La barretta mente in silenzio se il ritardo negativo si scollega da quando
 * il dato è stato letto: resta piena, e chi guarda crede che il tabellone si
 * sia appena riletto. Nessuno se ne accorgerebbe guardando lo schermo. */
const pezzoFreschezza = ritaglia('function rigaFreschezza()', '/* La rilettura di un treno');
const nuovaFreschezza = (n) => new Function('stato', 'eta', 'RINFRESCO', 'VECCHIA',
  `${pezzoFreschezza}; return ${n};`);

const RINFRESCO = 60_000;
const VECCHIA = 2 * 60_000;
const contesto = (ms, caricamento = false) =>
  [{ caricamento, scaricatoIl: ms === null ? 0 : Date.now() - ms },
   () => `${Math.round(ms / 60_000)} minuti fa`, RINFRESCO, VECCHIA];

const barra = (ms, caricamento = false) => nuovaFreschezza('barraCiclo')(...contesto(ms, caricamento))();
const testo = (ms, caricamento = false) => nuovaFreschezza('rigaFreschezza')(...contesto(ms, caricamento))();

const ritardo = (html) => Number((html.match(/--trascorso:(-?\d+)ms/) || [])[1]);

// Appena letto: barretta piena, cioè nessuno scorrimento già consumato.
assert.ok(Math.abs(ritardo(barra(0))) < 50, 'appena letto la barretta parte piena');

// A metà minuto deve partire da metà, non da capo.
const meta = ritardo(barra(30_000));
assert.ok(meta < -29_000 && meta > -31_000, `a metà minuto il ritardo è ${meta}`);

// Oltre il minuto — l'app è stata in secondo piano — si ferma a vuota invece
// di ripartire: un giro in più direbbe che il dato è appena arrivato.
assert.strictEqual(ritardo(barra(5 * 60_000)), -RINFRESCO, 'oltre il minuto resta a fondo corsa');

// Durante una rilettura la barretta resta: toglierla la farebbe lampeggiare
// via e tornare a ogni minuto. Prima della prima lettura invece non c'è.
assert.ok(/ciclo/.test(barra(60_000, true)), 'in rilettura la barretta resta');
assert.strictEqual(barra(null), '', 'prima della prima lettura niente barretta');

// Fresco il testo tace: la barretta conta già quel minuto, e ripeterlo a
// parole sarebbe la stessa cosa detta due volte.
assert.strictEqual(testo(1000), '', 'fresco non si dice');
assert.strictEqual(testo(60_000), '', 'nemmeno a un minuto, che è cadenza normale');
// Vecchio invece sì: è il caso in cui tacere farebbe passare il vecchio per nuovo.
assert.strictEqual(testo(4 * 60_000), 'letto <span id="eta">4 minuti fa</span>',
  'vecchio si dice, e resta agganciato a #eta perché continui a scorrere');
// "aggiornamento…" non si scrive più: comparendo e sparendo ogni minuto
// portava via una riga e faceva ballare il contenuto sotto.
assert.strictEqual(testo(1000, true), '', 'in rilettura il sottotitolo tace');
assert.strictEqual(testo(null), '', 'prima della prima lettura pure');

console.log('freschezza: ok — 10 casi su barretta e testo');


/* ------------------------------------- le due sezioni delle comunicazioni */

/* La pagina di Trenord tiene due elenchi — "STATO DELLA LINEA" e "AVVISI" — e
   arrivano mescolati in una lista sola. Qui si controlla che nella riga di una
   linea finisca la sola circolazione, dal più recente, e con l'ora. */

const sezioni = new Function(
  `${ritaglia('const SEZIONE_AVVISI', 'function vociAvviso')}
   return { diCircolazione, quandoAvviso };`)();

// I cinque avvisi veri della RE_5 dell'8 settembre 2026, mescolati come li
// manda Trenord: tre di circolazione fuori ordine, due programmati.
const re5 = [
  { date: '2026-09-08T16:41:00Z', section: 2, text: '2536 non è ancora partito' },
  { date: '2026-09-08T16:46:54Z', section: 2, text: '2506 oggi non partirà' },
  { date: '2026-09-08T16:42:48Z', section: 2, text: '2504 viaggia in ritardo' },
  { date: '2026-09-02T12:58:00Z', section: 1, text: 'variazioni fino al 13 settembre' },
  { date: '2026-09-01T16:29:27Z', section: 1, text: 'sciopero CUB e SGB' },
];

const tenuti = sezioni.diCircolazione(re5);
assert.strictEqual(tenuti.length, 3, 'restano le sole tre di circolazione');
assert.ok(!tenuti.some((a) => a.section === 1), 'nessun programmato fra i tenuti');
// Il più recente in cima: 18:46, poi 18:42, poi 18:41.
assert.deepStrictEqual(tenuti.map((a) => a.text), [
  '2506 oggi non partirà',
  '2504 viaggia in ritardo',
  '2536 non è ancora partito',
], 'ordinate dalla più recente');

// Una sezione che non conosciamo si tiene: se la sorgente cambia sotto, una
// riga in più è meglio di una notizia scomparsa in silenzio.
assert.strictEqual(
  sezioni.diCircolazione([{ date: '2026-09-08T10:00:00Z', text: 'x' }]).length, 1,
  'senza sezione si tiene');
assert.strictEqual(
  sezioni.diCircolazione([{ date: '2026-09-08T10:00:00Z', section: 7, text: 'x' }]).length, 1,
  'sezione sconosciuta si tiene');

// Senza data va in fondo, non in cima: non è il candidato a essere il più
// recente solo perché non si sa quando è stato scritto.
const senzaData = sezioni.diCircolazione([
  { section: 2, text: 'senza data' },
  { date: '2026-09-08T16:41:00Z', section: 2, text: 'con data' },
]);
assert.deepStrictEqual(senzaData.map((a) => a.text), ['con data', 'senza data'],
  'quella senza data resta in fondo');

/* --------------------------------------------- l'ora delle comunicazioni */

const quando = new Function(
  `${ritaglia('function quandoScritto', '/* Lo scheletro tiene')}; return quandoScritto;`)();

// Di oggi la sola ora: il giorno lo si sa, e ripeterlo su tre righe di seguito
// era proprio ciò che le rendeva indistinguibili.
const oggi = new Date();
oggi.setHours(18, 46, 0, 0);
assert.strictEqual(quando({ date: oggi.toISOString() }), '18:46', 'di oggi solo l\'ora');

// Dei giorni prima il giorno e l'ora, come scrive Trenord.
//
// La data si costruisce dai componenti locali e non da un istante UTC fisso:
// scritta come "2026-09-02T12:58:00Z" l'attesa valeva solo su una macchina in
// ora italiana, e il controllo cadeva sul runner, che sta in UTC. Del giorno si
// verifica la forma e non il nome, che è quello che il fuso non può spostare.
const vecchio = new Date();
vecchio.setDate(vecchio.getDate() - 6);
vecchio.setHours(14, 58, 0, 0);
assert.match(quando({ date: vecchio.toISOString() }), /^\d{1,2} [a-zà-ù]+, 14:58$/,
  'dei giorni prima giorno e ora');

// Senza data, o con una data che non si legge, niente etichetta: meglio la
// riga senza data che una data inventata.
assert.strictEqual(quando({}), '', 'senza data niente etichetta');
assert.strictEqual(quando({ date: 'non una data' }), '', 'data illeggibile: niente');

console.log('sezioni: ok — 8 casi sul filtro e l\'ordine + 4 sull\'ora');
