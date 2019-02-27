package actions

import (
	"strings"

	"github.com/alecthomas/participle/lexer"
)


func (as *ActionSuite) Test_Lexer() {
	as.LoadFixture("two simple accounts")

	goodInput := []string{
		"invite create    ",
		"invite create -user fred",
		"invite utilize -user fred \"74738ff5-5367-5958-9aee-98fffdcd1876\"",
		"invite retract \t -user fred something",
		"   invite retract --user=fred something",
		"",
	}

	badInput := []string{
		"invite create '",
		"invite create \\",
		"invite utilize -user fred \"74738ff5-5367-5958-9aee-98fffdcd1876",
		"%invite retract -user fred something",
		"invite retract --user+=fred",
	}

	for _, i := range goodInput {
		l, err := lexerDef.Lex(strings.NewReader(i))
		as.Nil(err)
		_, err = lexer.ConsumeAll(l)
		as.Nil(err)
	}

	for _, i := range badInput {
		l, err := lexerDef.Lex(strings.NewReader(i))
		as.Nil(err)
		_, err = lexer.ConsumeAll(l)
		as.NotNil(err)
	}
}

func (as *ActionSuite) Test_Parser() {
	cmd:=&Command{}
	goodInput:=[]string{
		"foo bar baz",
		"",
		"   ",
		"foo \"bar baz\" fleazil",
		"\"all the words\"",
	}
	badInput:=[]string{
		"foo bar 'baz'",
		";",
		"foo bar \" baz",
		"foo \"bar baz\" fleazil\"",
	}
	for _, i:=range goodInput{
		err :=commandParser.ParseString(i, cmd)
		as.Nil(err)
	}
	for _, i:=range badInput{
		err :=commandParser.ParseString(i, cmd)
		as.NotNil(err)
	}
}
