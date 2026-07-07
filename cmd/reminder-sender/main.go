package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
)

func main() {
	app, err := buildApp(context.Background())
	if err != nil {
		panic(fmt.Sprintf("failed to build app: %v", err))
	}

	lambda.Start(app.Handle)
}
