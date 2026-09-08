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

