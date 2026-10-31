package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"raftgo/client"
)

func ExampleClient() {
	a, err := client.New("http://localhost:8001", &client.Options{HTTPClient: &http.Client{Timeout: 7 * time.Second}, MaxRedirects: 5})
	if err != nil {
		panic(err)
	}
	b, err := client.New("http://localhost:8002", nil)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock, err := a.Lock(ctx, "message")
	if err != nil {
		fmt.Println(err)
		return
	}
	// The owner keeps the token explicitly. Client b does not acquire it.
	if _, err = b.Put(ctx, "message", "other", ""); errors.Is(err, client.ErrLocked) {
		fmt.Println("locked by a")
	}
	if _, err = a.Put(ctx, "message", "hello", lock.Token); err != nil {
		// An unknown outcome must be reconciled by the application, never retried
		// blindly. Do not release ownership while its write outcome is unresolved.
		fmt.Println(err)
		return
	}
	if _, err = a.CAS(ctx, "message", "hello", "world", lock.Token); err != nil {
		fmt.Println(err)
		return
	}
	if _, err = a.Unlock(ctx, "message", lock.Token); err != nil {
		fmt.Println(err)
	}
}
