package main

import (
	// Required dependencies for the Radiocheck worker
	_ "github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/nats-io/nats.go"
	_ "github.com/redis/go-redis/v9"
	_ "github.com/aws/aws-sdk-go-v2"
	_ "github.com/aws/aws-sdk-go-v2/config"
	_ "github.com/aws/aws-sdk-go-v2/credentials"
	_ "github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/google/uuid"
	_ "go.uber.org/zap"

	"fmt"
)

func main() {
	fmt.Println("Radiocheck worker - PoC Phase 1")
}
