package mazid

// wordlist1 contains 128 adjectives (2^7), positive and negative,
// selected for lower frequency of use.
var wordlist1 = [128]string{
	"avuncular", "genial", "sanguine", "ebullient", "magnanimous",
	"beneficent", "propitious", "auspicious", "salutary", "munificent",
	"affable", "convivial", "jovial", "buoyant", "blithe",
	"halcyon", "beatific", "felicitous", "sterling", "laudable",
	"exemplary", "peerless", "resplendent", "scintillating", "vivacious",
	"perspicacious", "sagacious", "judicious", "discerning", "luminous",
	"clement", "gregarious", "incandescent", "ineffable", "supernal",
	"pellucid", "lambent", "mellifluous", "dulcet", "redoubtable",
	"venerable", "stalwart", "indefatigable", "assiduous", "intrepid",
	"dauntless", "valiant", "estimable", "meritorious", "august",
	"illustrious", "effulgent", "coruscating", "numinous", "sublime",
	"transcendent", "fecund", "verdant", "limpid", "sprightly",
	"adroit", "dexterous", "canny", "perspicuous",
	// negative
	"perfidious", "mendacious", "venal", "nefarious", "iniquitous",
	"dissolute", "recalcitrant", "obstreperous", "truculent", "cantankerous",
	"querulous", "petulant", "churlish", "boorish", "craven",
	"ignoble", "ignominious", "odious", "execrable", "egregious",
	"pernicious", "insidious", "baleful", "malevolent", "vindictive",
	"callous", "remorseless", "obdurate", "bellicose", "captious",
	"sanctimonious", "duplicitous", "pusillanimous", "reprobate", "licentious",
	"profligate", "feckless", "otiose", "torpid", "vapid",
	"insipid", "meretricious", "specious", "fatuous", "nugatory",
	"vacuous", "puerile", "insolent", "imperious", "officious",
	"contumacious", "froward", "fractious", "censorious", "acrimonious",
	"vitriolic", "scurrilous", "invidious", "inimical", "deleterious",
	"noisome", "noxious", "sordid", "louche",
}

// wordlist2 contains 64 bands (2^6) from alternative, goth, synth pop,
// dream pop, and shoegaze genres, primarily 1978–2010.
var wordlist2 = [64]string{
	"cure", "depechemode", "neworder", "rem", "radiohead",
	"pixies", "smiths", "talkingheads", "sonicyouth", "joydivision",
	"bauhaus", "sistersofmercy", "siouxsieandbanshees", "petshopboys", "eurythmics",
	"tearsforfears", "humanleague", "erasure", "blur", "oasis",
	"portishead", "massiveattack", "groovearmada", "interpol", "killers",
	"m83", "beachhouse", "goldfrapp", "blondie", "devo",
	"xtc", "elviscostello", "echoandbunnymen", "mybloodvalentine", "cocteautwins",
	"deadcandance", "loveandrockets", "simpleminds", "duranduran", "cultureclub",
	"nickcaveandbadseeds", "birthdayparty", "killingjoke", "huskerdu", "mazzystar",
	"lush", "slowdive", "orchestralmanoeuvres", "inxs", "crowdedhouse",
	"nineinchnails", "ministry", "royksopp", "radiodept", "tameimpala",
	"bjork", "tonesontail", "thismortalcoil", "jesusandmarychain", "throwingmuses",
	"skinnypuppy", "sundays", "concreteblonde", "tacticalgere",
}

// wordlist3 contains 128 people (2^7): 64 European historical figures from
// roughly 1550–1730 followed by 64 notable footballers. Accents and
// punctuation are removed; compound names are concatenated.
var wordlist3 = [128]string{
	// Historical figures — French
	"richelieu", "colbert", "fouquet", "louvois", "vauban",
	"turenne", "conde", "luxembourg", "villars", "descartes",
	"pascal", "moliere", "racine", "corneille", "lafontaine",
	"boileau", "bossuet", "fenelon", "labruyere", "sevigne",
	"saintsimon", "retz", "perrault", "poussin", "lebrun",
	"lully", "couperin", "charpentier", "maintenon", "montespan",
	"lavalliere", "chevreuse", "longueville", "arnauld", "philippe",
	"gaston", "henriette",
	// Historical figures — British
	"cromwell", "milton", "locke", "newton", "hobbes",
	"boyle", "purcell", "halley",
	// Historical figures — Other Europeans
	"spinoza", "leibniz", "huygens", "rembrandt", "vermeer",
	"rubens", "christina", "kepler", "galileo", "tycho",
	"leeuwenhoek", "velazquez", "bernini", "stradivari", "torricelli",
	"bernoulli", "napier", "gregory", "paterson",
	// Footballers — Group 1 (exceptions + all-time greats)
	"maradona", "pele", "zidane", "cruyff", "beckenbauer", "platini",
	// Footballers — Group 2
	"henry", "cantona", "charlton", "moore", "best",
	"eusebio", "maldini", "baggio", "puskas",
	// Footballers — Group 3
	"beckham", "shearer", "rooney", "gerrard", "lineker",
	"mbappe", "griezmann", "raul", "xavi", "iniesta",
	"figo", "gullit", "baresi", "mueller", "shevchenko",
	// Footballers — Group 4
	"thuram", "desailly", "deschamps", "papin", "vieira",
	"gascoigne", "greaves", "lampard", "scholes", "owen",
	"giggs", "dalglish", "law", "rummenigge", "matthaeus",
	"kahn", "klose", "neuer", "vanbasten", "bergkamp",
	"robben", "rijkaard", "totti", "delpiero", "buffon",
	"pirlo", "pagliuca", "ronaldo", "modric", "hagi",
	"casillas", "torres", "villa", "stoichkov",
}
