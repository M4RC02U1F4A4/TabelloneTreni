# Tabellone Treni

Una versione per telefono dei tabelloni di [RFI](https://iechub.rfi.it/ArriviPartenze),
con in più la cosa che al sito originale manca: **il filtro per dove devi
andare**. Scegli partenza e arrivo e vedi solo i treni che fermano davvero lì,
con l'orario a cui ci arrivano.

- **due ritardi per treno**: quello del tabellone RFI e quello misurato sul treno da ViaggiaTreno, che non dicono la stessa cosa
- si aggiorna da solo una volta al minuto, e si ferma quando la pagina non è in primo piano
- le tratte si salvano fra i preferiti e stanno in cima alla home
- installabile sulla schermata iniziale del telefono
- immagine Docker da 9 MB, nessun database, nessuno stato su disco

|  |  |
|:--|:--|
| <img width="330" src="docs/home.png" alt="La home con quattro tratte fra i preferiti e le due voci per il tabellone di una stazione"> | <img width="330" src="docs/tratta.png" alt="I treni da Milano Porta Garibaldi che fermano a Milano Rogoredo, con l'elenco fermate aperto"> |
| **Le tratte salvate stanno in cima**, e si aprono con un tocco; sotto, il tabellone intero di una stazione, partenze o arrivi. | **Solo i treni che fermano dove vai**, con l'ora a cui ci arrivano. Toccando il treno si aprono le fermate, con la tua evidenziata. |
| <img width="330" src="docs/tabellone.png" alt="Il tabellone completo delle partenze da Milano Porta Garibaldi, con i due ritardi affiancati su ogni treno"> | <img width="330" src="docs/ricerca.png" alt="La ricerca stazione con la corrispondenza evidenziata in mezzo al nome"> |
| **Il tabellone completo**: binari, soppressioni, avvisi e i **due ritardi** affiancati — in cima, un treno che per RFI ne ha 70 e per ViaggiaTreno 95. | **La ricerca** cerca dentro il nome, non solo all'inizio, e non tocca la rete. |

## Come funziona

Il tabellone di RFI è HTML renderizzato dal server: non esiste un endpoint JSON
e il suo JavaScript non fa nemmeno una chiamata di rete. Il server qui dentro lo
scarica, lo interpreta e ne serve i dati.

Il motivo per cui il server esiste sono due cose che il browser non può
aggirare: RFI non manda intestazioni CORS, e la sua pagina pesa circa 280 KB che
**non comprime nemmeno se gliela si chiede compressa**. Ridotta a dati e
gzippata, diventa ~3 KB per un tabellone intero e ~400 byte per uno filtrato.

Il filtro per destinazione costa una sola richiesta per aggiornamento, perché
ogni partenza porta già con sé l'elenco completo delle fermate successive.

I tabelloni stanno in cache 30 secondi, presi sotto un lock per stazione: dieci
persone sulla stessa stazione producono comunque una richiesta sola verso RFI
ogni mezzo minuto. Se RFI smette di rispondere, per un minuto viene servito il
tabellone scaduto invece di un errore.

### I due ritardi

RFI pubblica il proprio ritardo con parsimonia, e sbaglia in due modi diversi.
Campione preso a Milano Porta Garibaldi alle 21:48: sui 20 treni presenti in
entrambe le fonti, ViaggiaTreno ne aveva sei con una misura vera, e su **cinque
di quei sei il tabellone diceva zero** mentre il treno viaggiava a +2, +2, +1,
−1, −1. Il sesto era un treno molto in ritardo: **RFI ne dichiarava 70, il treno
ne aveva 95**.

Sotto i pochi minuti il tabellone arrotonda a zero, e proprio lì si decide se il
treno si prende; sopra l'ora, si aggiorna con calma, e venticinque minuti di
differenza cambiano la sera.

Quale delle due letture sia quella giusta non lo decide l'app: le mostra
tutt'e due, una accanto all'altra, e il colore dice da dove viene il numero.
Che il treno sia in ritardo lo dice invece l'orario, che diventa rosso.

| Pastiglia | Campo JSON | Sorgente |
| --- | --- | --- |
| ambra | `delay` | la cella "ritardo" del tabellone RFI |
| ciano | `liveDelay` | `partenze`/`arrivi` di ViaggiaTreno, per la stessa stazione |

Le due fonti si accoppiano sul **numero del treno**, l'unico identificatore che
condividono. È anche una chiave prudente: lo stesso numero su due tabelloni
vicini è lo stesso treno, quindi un accoppiamento sbagliato fra stazioni non
trova niente invece di mostrare il ritardo di un altro treno.

**La pastiglia ciano manca finché il treno non è stato rilevato.** ViaggiaTreno
manda `ritardo: 0` anche per un treno che non è ancora partito: è il valore di
partenza del campo, non una misura. A separare i due casi è `compRitardo`, che
sul primo dice "non partito" e sul secondo "in orario" — e uno zero mostrato
come puntualità sarebbe una puntualità che nessuno ha visto. Un ritardo diverso
da zero, invece, vale come misura comunque, così la regola non si rompe il
giorno che compare una dicitura nuova.

La lettura di ViaggiaTreno è **facoltativa in ogni punto**: parte in parallelo a
quella di RFI (non in fila, altrimenti la pagina aspetterebbe la somma di due
servizi lenti), va nella stessa cache da 30 secondi, e se fallisce o se la
stazione non ha un codice ViaggiaTreno il tabellone esce come prima, con il solo
ritardo di RFI.

### Il riconoscimento delle fermate

I nomi delle fermate sul tabellone sono abbreviati e non combaciano con quelli
del catalogo: `MI BOVISA P.` sta per `MILANO BOVISA POLITECNICO`. Si risolvono
su due livelli:

1. i **nomi brevi di ViaggiaTreno**, uniti al catalogo una volta sola quando lo
   si genera, che coprono le contrazioni (`BOLOGNA C.LE` → `BOLOGNA CENTRALE`);
2. un confronto **token per token, per prefisso**, che copre i troncamenti
   (`GAZZADA SCHIAN.M` → `GAZZADA SCHIANNO MORAZZONE`).

Il confronto richiede lo stesso numero di token da entrambe le parti. È questo
vincolo a impedire che `LODI` catturi `LODI VECCHIO`: un falso positivo qui
farebbe salire qualcuno sul treno sbagliato, quindi la regola resta stretta e le
eccezioni vere si aggiungono a mano in `cmd/genstations/main.go`.

## Farlo girare

```sh
docker run -p 8080:8080 ghcr.io/m4rc02u1f4a4/tabellonetreni:latest
```

oppure `docker compose up -d`. In sviluppo basta `go run .` — l'interfaccia è
HTML, CSS e JavaScript senza passo di build, quindi non serve Node.

| variabile | difetto | |
|---|---|---|
| `PORT` | `8080` | porta di ascolto |
| `ADDR` | `:8080` | indirizzo completo, ha la precedenza su `PORT` |

## Aggiornare il catalogo delle stazioni

Il catalogo (2435 stazioni con i loro alias e, per 2423 di esse, il codice
ViaggiaTreno da cui si leggono i ritardi) è committato in
`internal/stations/stations.json` ed embeddato nel binario: il server parte
istantaneamente e non dipende da due siti esterni per riuscire ad avviarsi.
Cambia molto di rado; per rigenerarlo:

```sh
go run ./cmd/genstations
```

## Limiti noti

- **Gli arrivi non si possono filtrare.** RFI pubblica le fermate successive
  solo sui tabelloni delle partenze. Chiedendo un filtro su un tabellone arrivi,
  l'app mostra tutti gli arrivi e lo dice.
- **Alcune stazioni non ci sono.** Il listino RFI non comprende la rete
  Ferrovienord (Saronno, Castellanza), quella svizzera oltre Chiasso, né
  impianti come Malpensa Aeroporto e Milano Bovisa Politecnico. Compaiono come
  fermate dei treni ma non sono selezionabili.
- **Le stazioni senza codice ViaggiaTreno restano al solo tabellone.** Sono
  dodici su 2435, quelle il cui nome non si accoppia con nessuna voce
  dell'elenco di ViaggiaTreno: lì la pastiglia ciano non compare mai.
- **Il markup di RFI può cambiare senza preavviso.** I test girano su pagine
  reali salvate in `internal/rfi/testdata`: se si rompono senza che sia cambiato
  il codice, è cambiato il sito.

## Rilasci

Ogni commit su `main` fa una versione: `feat:` alza la minor, `fix:` e `perf:`
la patch, un `!` o un `BREAKING CHANGE:` la major, il resto vale patch. Il
workflow crea il tag (senza prefisso `v`), pubblica la release e spinge
l'immagine multi-architettura su GHCR.
