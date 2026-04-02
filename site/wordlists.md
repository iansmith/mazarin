# Maz-ID Wordlists

Maz-ID generates human-readable identifiers of the form:

```
{adjective}-{band}-{person}
```

Each component is drawn from one of three wordlists:

| Wordlist | Category | Entries | Bits |
|---|---|---|---|
| wordlist1 | Adjectives | 128 | 7 |
| wordlist2 | Bands | 64 | 6 |
| wordlist3 | People | 128 | 7 |

Total unique IDs: 128 × 64 × 128 = **2^20 = 1,048,576**

Bit layout of a uint32 ID:

```
bits  0– 6   wordlist3 index (person,    7 bits, 128 values)
bits  7–12   wordlist2 index (band,      6 bits,  64 values)
bits 13–19   wordlist1 index (adjective, 7 bits, 128 values)
bits 20–31   unused (always zero)
```

---

## Wordlist 1 — Adjectives (128)

Words are positive or negative adjectives, selected for lower frequency of use.
Common words like *good*, *bad*, *happy* were passed over in favour of rarer ones.

### Positive

avuncular
: kindly, like a benevolent uncle

genial
: warmly friendly

sanguine
: optimistic, confident

ebullient
: overflowing with enthusiasm

magnanimous
: noble and generous in spirit

beneficent
: actively doing good

propitious
: favorable, of good omen

auspicious
: promising success

salutary
: producing good effects

munificent
: extravagantly generous

affable
: easy and pleasant to talk to

convivial
: friendly and fond of festivity

jovial
: cheerful and friendly

buoyant
: lighthearted, optimistic

blithe
: happily carefree

halcyon
: idyllically happy and peaceful

beatific
: blissfully happy, saintly

felicitous
: well-chosen, pleasingly apt

sterling
: of the highest quality

laudable
: worthy of praise

exemplary
: ideal, serving as a fine model

peerless
: having no equal

resplendent
: impressively bright and beautiful

scintillating
: sparkling with brilliance and wit

vivacious
: charmingly lively

perspicacious
: keenly insightful

sagacious
: profoundly wise

judicious
: having sound judgment

discerning
: perceptive, showing good taste

luminous
: radiant, glowing

clement
: mild, merciful, lenient

gregarious
: warmly sociable

incandescent
: brilliantly luminous; intensely passionate

ineffable
: too beautiful or great for words

supernal
: heavenly, celestial

pellucid
: crystal clear, perfectly expressed

lambent
: softly bright, flickering gently

mellifluous
: flowing sweetly, like honey

dulcet
: sweet and melodious

redoubtable
: formidably impressive

venerable
: commanding respect by virtue and wisdom

stalwart
: strong, loyal, reliable

indefatigable
: tireless

assiduous
: diligent, hard-working

intrepid
: fearlessly adventurous

dauntless
: unflinchingly brave

valiant
: courageous, heroic

estimable
: highly regarded

meritorious
: deserving of high praise

august
: inspiring reverence, majestic

illustrious
: greatly distinguished

effulgent
: radiant, dazzling

coruscating
: sparkling; also: scathingly brilliant (British usage)

numinous
: awe-inspiring, spiritually powerful

sublime
: transcendently beautiful or excellent

transcendent
: surpassing all others

fecund
: fertile, richly productive

verdant
: lush and green; flourishing

limpid
: clear, calm, and serene

sprightly
: lively and full of spirit

adroit
: cleverly skillful

dexterous
: nimble and skillful

canny
: shrewd and careful

perspicuous
: clear and easily understood

### Negative

perfidious
: deceitfully treacherous

mendacious
: habitually untruthful

venal
: corruptly open to bribery

nefarious
: wicked, criminal

iniquitous
: grossly unjust

dissolute
: morally unrestrained

recalcitrant
: stubbornly defiant

obstreperous
: noisily unruly

truculent
: aggressively confrontational

cantankerous
: ill-tempered and argumentative

querulous
: whining and complaining

petulant
: sulky, childishly bad-tempered

churlish
: surly and rude

boorish
: crude and ill-mannered

craven
: shamefully cowardly

ignoble
: dishonourable, base

ignominious
: publicly disgraceful

odious
: deeply repugnant

execrable
: appallingly bad

egregious
: shockingly bad, flagrant

pernicious
: causing insidious harm

insidious
: subtly and gradually harmful

baleful
: menacingly harmful

malevolent
: deeply malicious

vindictive
: obsessively vengeful

callous
: cruelly indifferent

remorseless
: pitilessly relentless

obdurate
: stubbornly hardhearted

bellicose
: aggressively warlike

captious
: habitually fault-finding

sanctimonious
: smugly self-righteous

duplicitous
: deliberately deceptive

pusillanimous
: contemptibly cowardly

reprobate
: morally depraved

licentious
: sexually or morally unrestrained

profligate
: shamelessly dissolute

feckless
: uselessly irresponsible

otiose
: pointlessly idle

torpid
: sluggish, apathetic

vapid
: tediously empty

insipid
: bland and characterless

meretricious
: flashily superficial, cheaply attractive

specious
: plausible but false

fatuous
: smug and pointlessly silly

nugatory
: worthless, trifling

vacuous
: mindlessly empty

puerile
: childishly silly

insolent
: offensively disrespectful

imperious
: arrogantly domineering

officious
: intrusively bossy

contumacious
: stubbornly insubordinate

froward
: habitually contrary; archaic but perfect

fractious
: unruly and irritable

censorious
: harshly judgmental

acrimonious
: bitterly hostile

vitriolic
: savagely harsh

scurrilous
: outrageously defamatory

invidious
: unfairly discriminatory; likely to cause resentment

inimical
: actively harmful or hostile

deleterious
: subtly damaging

noisome
: note — not "noisy"; means noxious or offensive

noxious
: harmful and unpleasant

sordid
: involving ignoble or immoral dealings

louche
: shiftily disreputable

---

## Wordlist 2 — Bands (64)

Bands from alternative, goth, synth pop, dream pop, and shoegaze genres,
primarily 1978–2010. More popular bands are favoured over obscure ones.
Multi-word names are concatenated; leading articles are dropped
(*The Cure* → `cure`, *A Flock of Seagulls* → `flockofseagulls`).

### Tier 1

cure
: English · 1976–present · godfathers of goth

depechemode
: English · 1980–present · synth pop icons

neworder
: English · 1980–present · post-Joy Division, synth and dance

rem
: American · 1980–2011 · alternative rock icons

radiohead
: English · 1985–present · alternative and art rock

pixies
: American · 1986–present · hugely influential alternative

smiths
: English · 1982–1987 · definitive 80s alternative

talkingheads
: American · 1975–1991 · new wave and art rock

sonicyouth
: American · 1981–2011 · noise rock and alternative

joydivision
: English · 1976–1980 · post-punk, goth progenitors

bauhaus
: English · 1978–1983 · goth founders

sistersofmercy
: English · 1980–present · goth pillars

siouxsieandbanshees
: English · 1976–1996 · post-punk and goth

petshopboys
: English · 1981–present · synth pop

eurythmics
: Scottish · 1980–1990 · synth pop

tearsforfears
: English · 1981–present · synth pop

humanleague
: English · 1977–present · synth pop pioneers

erasure
: English · 1985–present · synth pop

blur
: English · 1988–present · Britpop and alternative

oasis
: English · 1991–2009 · Britpop

portishead
: English · 1991–present · trip hop

massiveattack
: English · 1988–present · trip hop and alternative

groovearmada
: English · 1997–present · electronic and trip hop

interpol
: American · 2002–present · post-punk revival

killers
: American · 2001–present · synth-influenced alternative

m83
: French · 2001–present · dream pop and synth

beachhouse
: American · 2004–present · dream pop

goldfrapp
: English · 1999–present · synth pop and electronic

### Tier 2

blondie
: American · 1974–present · proto new wave

devo
: American · 1973–present · new wave and art rock

xtc
: English · 1972–1999 · new wave and art pop

elviscostello
: English · 1977–present · new wave and pub rock

echoandbunnymen
: English · 1978–present · post-punk

mybloodvalentine
: Irish · 1983–present · shoegaze founders

cocteautwins
: Scottish · 1979–1997 · dream pop

deadcandance
: Australian · 1981–present · goth and darkwave

loveandrockets
: English · 1985–1999 · Bauhaus offshoot, goth and alternative

simpleminds
: Scottish · 1977–present · synth pop and alternative

duranduran
: English · 1978–present · synth pop and new wave

cultureclub
: English · 1981–1986 · new wave

nickcaveandbadseeds
: Australian · 1983–present · post-punk and gothic

birthdayparty
: Australian · 1977–1983 · Nick Cave's earlier band, post-punk and goth

killingjoke
: English · 1978–present · post-punk and industrial

huskerdu
: American · 1979–1988 · hardcore and alternative

mazzystar
: American · 1989–present · dream pop

lush
: English · 1987–1996 · shoegaze

slowdive
: English · 1989–2014 · shoegaze

orchestralmanoeuvres
: English · 1978–present · synth pop (OMD)

inxs
: Australian · 1977–2012 · alternative and new wave

crowdedhouse
: Australian/New Zealand · 1985–present · alternative pop

nineinchnails
: American · 1988–present · industrial and alternative

ministry
: American · 1981–present · industrial and synth

royksopp
: Norwegian · 1998–present · synth pop and electronic

radiodept
: Swedish · 2000–present · dream pop

tameimpala
: Australian · 2007–present · psychedelic and dream pop

bjork
: Icelandic · 1993–present (solo) · alternative and electronic

### Tier 3

tonesontail
: English · 1982–1984 · goth and post-punk, Bauhaus offshoot

thismortalcoil
: English · 1983–1991 · darkwave and dream pop, 4AD collective

jesusandmarychain
: Scottish · 1983–present · noise pop and shoegaze

throwingmuses
: American · 1983–2003 · alternative, Kristin Hersh's band

skinnypuppy
: Canadian · 1982–present · industrial and synth

sundays
: English · 1987–1997 · dream pop and alternative

concreteblonde
: American · 1982–2001 · alternative rock

tacticalgere
: · tier 3 by popular demand

---

## Wordlist 3 — People (128)

Two groups of 64: European historical figures from roughly 1550–1730,
and notable footballers spanning roughly 1950–present. French figures and
British/Irish figures are strongly preferred in both halves. Accents and
punctuation are removed; compound names are concatenated. Footballers
use surnames only, except those who go by a single name (Pelé, Eusébio, Raúl, Xavi).

### Historical Figures — French (37)

richelieu
: French · 1585–1642 · Cardinal, Louis XIII's great minister, architect of French absolutism

colbert
: French · 1619–1683 · Louis XIV's finance minister, built the economy and navy

fouquet
: French · 1615–1680 · finance minister, imprisoned for outshining the king

louvois
: French · 1641–1691 · minister of war, ruthless modernizer of the French army

vauban
: French · 1633–1707 · military engineer, designed fortifications across Europe

turenne
: French · 1611–1675 · marshal, widely regarded as the greatest general of the era

conde
: French · 1621–1686 · "The Great Condé", brilliant general, led the Fronde

luxembourg
: French · 1628–1695 · marshal under Louis XIV, won nearly every battle he fought

villars
: French · 1653–1734 · last great marshal of Louis XIV, saved France at Denain 1712

descartes
: French · 1596–1650 · philosopher and mathematician, "cogito ergo sum"

pascal
: French · 1623–1662 · mathematician, physicist, religious thinker

moliere
: French · 1622–1673 · comic playwright, Tartuffe, The Misanthrope

racine
: French · 1639–1699 · tragic playwright, pinnacle of French classicism

corneille
: French · 1606–1684 · playwright, Le Cid, older rival of Racine

lafontaine
: French · 1621–1695 · fabulist, Fables in verse

boileau
: French · 1636–1711 · poet and critic, the era's literary arbiter

bossuet
: French · 1627–1704 · bishop, greatest preacher of the age

fenelon
: French · 1651–1715 · archbishop, Télémaque, fell from royal favour

labruyere
: French · 1645–1696 · moralist, Les Caractères, sharp observer of court society

sevigne
: French · 1626–1696 · Madame de Sévigné, letter writer, vivid portrait of court life

saintsimon
: French · 1675–1755 · Duke of Saint-Simon, acid court memoirist

retz
: French · 1613–1679 · Cardinal de Retz, Fronde agitator and brilliant memoirist

perrault
: French · 1628–1703 · gave us Cinderella, Sleeping Beauty, Puss in Boots

poussin
: French · 1594–1665 · painter, classical landscapes, worked mostly in Rome

lebrun
: French · 1619–1690 · court painter, designed the Hall of Mirrors at Versailles

lully
: Italian-born French · 1632–1687 · composer who dominated French opera under Louis XIV

couperin
: French · 1668–1733 · "le Grand", keyboard composer and court musician

charpentier
: French · 1645–1704 · composer, Lully's great rival

maintenon
: French · 1635–1719 · Françoise d'Aubigné, Louis XIV's secret second wife

montespan
: French · 1640–1707 · Marquise de Montespan, Louis XIV's great mistress

lavalliere
: French · 1644–1710 · Louise de la Vallière, Louis XIV's earlier mistress, later became a nun

chevreuse
: French · 1600–1679 · Duchess of Chevreuse, inveterate conspirator against Richelieu and Mazarin

longueville
: French · 1619–1679 · Duchess of Longueville, Fronde leader, later retreated to Port-Royal

arnauld
: French · 1612–1694 · "Great Arnauld", Jansenist theologian and polemicist

philippe
: French · 1640–1701 · Philippe I, Duke of Orléans, Louis XIV's flamboyant brother

gaston
: French · 1608–1660 · Gaston of Orléans, Louis XIII's brother, serial rebel against Richelieu

henriette
: Anglo-French · 1644–1670 · Duchess of Orléans, died mysteriously at twenty-six

### Historical Figures — British (8)

cromwell
: English · 1599–1658 · Lord Protector, won the Civil War, ruled England 1653–1658

milton
: English · 1608–1674 · poet, Paradise Lost, blind when he wrote it

locke
: English · 1632–1704 · philosopher, founded liberal political theory

newton
: English · 1643–1727 · physicist, calculus, gravity — arguably the greatest scientist

hobbes
: English · 1588–1679 · philosopher, Leviathan, "solitary, poor, nasty, brutish, and short"

boyle
: Anglo-Irish · 1627–1691 · chemist, Boyle's Law, founded modern chemistry

purcell
: English · 1659–1695 · Dido and Aeneas, greatest English baroque composer

halley
: English · 1656–1742 · astronomer, predicted the comet that bears his name

### Historical Figures — Other Europeans (19)

spinoza
: Dutch · 1632–1677 · philosopher, excommunicated for his ideas

leibniz
: German · 1646–1716 · philosopher and mathematician, invented calculus independently of Newton

huygens
: Dutch · 1629–1695 · physicist, wave theory of light, pendulum clock, worked in Paris

rembrandt
: Dutch · 1606–1669 · painter, greatest portraitist of the era

vermeer
: Dutch · 1632–1675 · painter, Girl with a Pearl Earring

rubens
: Flemish · 1577–1640 · painter, prolific baroque master

christina
: Swedish · 1626–1689 · Queen of Sweden, abdicated, converted to Catholicism, moved to Rome

kepler
: German · 1571–1630 · astronomer, laws of planetary motion

galileo
: Italian · 1564–1642 · astronomer and physicist, condemned by the Inquisition

tycho
: Danish · 1546–1601 · Tycho Brahe, astronomer, wore a gold prosthetic nose

leeuwenhoek
: Dutch · 1632–1723 · microscopist, first to observe bacteria

velazquez
: Spanish · 1599–1660 · painter, Las Meninas

bernini
: Italian · 1598–1680 · sculptor and architect, St Peter's Square

stradivari
: Italian · 1644–1737 · violin maker, instruments still played in concert halls today

torricelli
: Italian · 1608–1647 · physicist, invented the barometer

bernoulli
: Swiss · 1655–1705 · Jakob Bernoulli, mathematician, fluid dynamics and probability

napier
: Scottish · 1550–1617 · John Napier, inventor of logarithms

gregory
: Scottish · 1638–1675 · James Gregory, mathematician, invented the reflecting telescope

paterson
: Scottish · 1658–1719 · William Paterson, founder of the Bank of England

### Footballers — Group 1 (all-time greats)

maradona
: Argentine · 1960–2020 · hand of God, greatest individual talent

pele
: Brazilian · 1940–2022 · three World Cups, arguably the greatest ever

zidane
: French · 1972– · possibly the greatest of his era, 1998 World Cup

cruyff
: Dutch · 1947–2016 · most influential player ever, Total Football

beckenbauer
: German · 1945–2024 · "Der Kaiser", invented the attacking libero

platini
: French · 1955– · three consecutive Ballon d'Or, 1984 Euros

### Footballers — Group 2

henry
: French · 1977– · Arsenal's greatest ever, 1998 and 2000 champion

cantona
: French · 1966– · Man Utd icon, reinvented English football

charlton
: English · 1937–2023 · 1966 World Cup, Man Utd European Cup

moore
: English · 1941–1993 · 1966 World Cup captain, greatest English defender

best
: Northern Irish · 1946–2005 · "the fifth Beatle", Man Utd

eusebio
: Portuguese · 1942–2014 · one name, 1966 World Cup top scorer, Benfica legend

maldini
: Italian · 1968– · greatest defender of his generation, AC Milan

baggio
: Italian · 1967– · "Il Divin Codino", Ballon d'Or 1993

puskas
: Hungarian/Spanish · 1927–2006 · Real Madrid and Hungary, one of the greatest scorers ever

### Footballers — Group 3

beckham
: English · 1975– · global icon, free-kick specialist

shearer
: English · 1970– · Premier League all-time top scorer

rooney
: English · 1985– · England all-time top scorer

gerrard
: English · 1980– · Liverpool legend, never won the league

lineker
: English · 1960– · 1986 World Cup Golden Boot, never booked

mbappe
: French · 1998– · current superstar, 2018 World Cup winner

griezmann
: French · 1991– · 2018 World Cup winner, Atlético legend

raul
: Spanish · 1977– · one name, Real Madrid all-time legend

xavi
: Spanish · 1980– · one name, tiki-taka architect, 2010 World Cup

iniesta
: Spanish · 1984– · 2010 World Cup final goal

figo
: Portuguese · 1972– · Ballon d'Or 2000, controversial Real Madrid move

gullit
: Dutch · 1962– · 1988 Euros, Ballon d'Or 1987

baresi
: Italian · 1960– · AC Milan legendary libero, 1994 World Cup

mueller
: German · 1945–2021 · "Der Bomber", 68 international goals, WC 1974

shevchenko
: Ukrainian · 1976– · Ballon d'Or 2004, AC Milan

### Footballers — Group 4

thuram
: French · 1972– · 1998 World Cup semi-final, two goals

desailly
: French · 1968– · 1998 World Cup, AC Milan and Chelsea

deschamps
: French · 1968– · 1998 World Cup player, greatest French coach

papin
: French · 1963– · Ballon d'Or 1991, Marseille legend

vieira
: French · 1976– · Arsenal and France, dominant midfield presence

gascoigne
: English · 1967– · "Gazza", greatest English talent of his generation

greaves
: English · 1940–2021 · 44 international goals, most natural finisher

lampard
: English · 1978– · Chelsea legend, 211 Premier League goals

scholes
: English · 1974– · Man Utd, Zidane called him best of his generation

owen
: English · 1979– · Ballon d'Or 2001, Real Madrid

giggs
: Welsh · 1973– · Man Utd, 13 Premier League titles

dalglish
: Scottish · 1951– · Liverpool icon as player and manager

law
: Scottish · 1940– · Man Utd, 1964 Ballon d'Or

rummenigge
: German · 1955– · 1980s Bayern Munich and Germany captain

matthaeus
: German · 1961– · most capped German, 1990 World Cup, Ballon d'Or 1990

kahn
: German · 1969– · legendary goalkeeper, 2002 World Cup Golden Ball

klose
: German · 1978– · all-time World Cup top scorer, 16 goals

neuer
: German · 1986– · redefined goalkeeping, sweeper-keeper

vanbasten
: Dutch · 1964– · possibly the greatest striker ever, three Ballon d'Or

bergkamp
: Dutch · 1969– · Arsenal legend, genius of touch and vision

robben
: Dutch · 1984– · Bayern Munich and Netherlands

rijkaard
: Dutch · 1962– · Netherlands 1988, later great Barcelona coach

totti
: Italian · 1976– · Roma, one-club legend, 307 Serie A goals

delpiero
: Italian · 1974– · Juventus legend, 2006 World Cup

buffon
: Italian · 1978– · greatest goalkeeper of his era, 2006 World Cup

pirlo
: Italian · 1979– · elegant deep playmaker, 2006 World Cup

pagliuca
: Italian · 1966– · Italy 1994 World Cup goalkeeper

ronaldo
: Portuguese · 1985– · GOAT debate, five Ballon d'Or

modric
: Croatian · 1985– · Ballon d'Or 2018, broke Messi/Ronaldo hegemony

hagi
: Romanian · 1965– · "the Maradona of the Carpathians", Barcelona and Galatasaray

casillas
: Spanish · 1981– · legendary goalkeeper, 2010 World Cup

torres
: Spanish · 1984– · peak at Liverpool was extraordinary, 2010 World Cup

villa
: Spanish · 1981– · Spain all-time top scorer, 2010 World Cup

stoichkov
: Bulgarian · 1966– · Ballon d'Or 1994, Barcelona under Cruyff
