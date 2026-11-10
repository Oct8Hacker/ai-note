//go:build integration

package worker

import (
	"context"
	"fmt"
	"os"
	"time"

	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	redisAddr  string
	pgAdminURL string
	amqpURL    string
)

// TestMain starts one Postgres, one RabbitMQ and one Redis container shared
// by every integration test in the package; each test gets its own database.
func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	ctx := context.Background()

	pg, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("postgres"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(pg) }()
	if pgAdminURL, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintln(os.Stderr, "postgres url:", err)
		return 1
	}

	rmq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:3.13-management",
			ExposedPorts: []string{"5672/tcp"},
			WaitingFor:   wait.ForLog("Server startup complete").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start rabbitmq:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(rmq) }()
	host, _ := rmq.Host(ctx)
	port, err := rmq.MappedPort(ctx, "5672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "rabbitmq port:", err)
		return 1
	}
	amqpURL = fmt.Sprintf("amqp://guest:guest@%s:%s/", host, port.Port())

	rds, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start redis:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(rds) }()
	rhost, _ := rds.Host(ctx)
	rport, err := rds.MappedPort(ctx, "6379/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "redis port:", err)
		return 1
	}
	redisAddr = fmt.Sprintf("%s:%s", rhost, rport.Port())

	return m.Run()
}
