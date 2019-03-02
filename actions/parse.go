package actions

import (
	"bytes"
	"fmt"

	"github.com/alecthomas/participle"
	"github.com/alecthomas/participle/lexer"
	"github.com/alecthomas/participle/lexer/ebnf"
	"github.com/gobuffalo/buffalo"
)

func init() {

	lexerDef = lexer.Must(ebnf.New(`
    Ident = (alpha | "_") { "_" | alpha | digit } .
    Number = ("." | digit) {"." | digit} .
    Whitespace = " " | "\t"  .
    Dash = "-" .
    Equals = "=" .
    Quoted = "\"" { "\u0000"…"\uffff" -"\"" -"\\" | "\\" any } "\"" .

    alpha = "a"…"z" | "A"…"Z" .
    digit = "0"…"9" .
    any = "\u0000"…"\uffff" .
`))

	commandParser = participle.MustBuild(
		&Command{},
		participle.Lexer(lexerDef),
		participle.Unquote("Quoted"),
		participle.Elide("Whitespace"),
	)
}

var lexerDef lexer.Definition
var commandParser *participle.Parser

type Command struct {
	Word []string `( @Ident | @Quoted)* `
}

/*
type Word struct {
	Word string `@Ident | @Quoted`
}*/

func printTokens(c buffalo.Context, original string, tok []lexer.Token) {
	var buf bytes.Buffer

	for _, t := range tok {
		buf.WriteString(fmt.Sprintf("[%#v] ", t))
		// _, err := &buf.WriteString(fmt.Sprintf("[%v] ", t))
		// if err != nil {
		// 	log.Fatalf("unable to write string to buffer: %v", err)
		// }
	}
	c.Logger().Infof("original: %s, tokenized: %s\n", original, buf.String())
}
