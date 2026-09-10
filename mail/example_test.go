package mail_test

import (
	"context"
	"fmt"

	"github.com/usefabrin/fabrin/mail"
)

func ExampleCapture() {
	inbox, err := mail.NewCapture(10)
	if err != nil {
		panic(err)
	}
	err = inbox.Send(context.Background(), mail.Message{
		To:      "person@example.com",
		Subject: "Sign in",
		Text:    "This is a test message.",
	})
	if err != nil {
		panic(err)
	}
	messages := inbox.Drain()
	fmt.Println(len(messages))
	fmt.Println(len(inbox.Messages()))
	// Output:
	// 1
	// 0
}
