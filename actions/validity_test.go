package actions

//Test_HasPaid checks that the account associated with a user has
//positive credits.
func (as *ActionSuite) Test_HasPaid() {
	as.LoadFixture("two simple accounts")

	paid, err := HasPaid("SLACKUSER1")
	as.NoError(err)
	as.False(paid)

	paid, err = HasPaid("SLACKUSER3")
	as.NoError(err)
	as.True(paid)

	_, err = HasPaid("other")
	as.NotNil(err)
}
