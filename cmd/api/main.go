package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HK9750/venueos/internal/access"
	accesspostgres "github.com/HK9750/venueos/internal/access/postgres"
	"github.com/HK9750/venueos/internal/audit"
	"github.com/HK9750/venueos/internal/channel"
	channelpostgres "github.com/HK9750/venueos/internal/channel/postgres"
	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/device"
	devicepostgres "github.com/HK9750/venueos/internal/device/postgres"
	"github.com/HK9750/venueos/internal/entry"
	entrypostgres "github.com/HK9750/venueos/internal/entry/postgres"
	"github.com/HK9750/venueos/internal/event"
	eventpostgres "github.com/HK9750/venueos/internal/event/postgres"
	"github.com/HK9750/venueos/internal/httpapi"
	"github.com/HK9750/venueos/internal/inventory"
	inventorypostgres "github.com/HK9750/venueos/internal/inventory/postgres"
	"github.com/HK9750/venueos/internal/invitation"
	invitationpostgres "github.com/HK9750/venueos/internal/invitation/postgres"
	"github.com/HK9750/venueos/internal/logging"
	"github.com/HK9750/venueos/internal/membership"
	membershippostgres "github.com/HK9750/venueos/internal/membership/postgres"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/organization"
	organizationpostgres "github.com/HK9750/venueos/internal/organization/postgres"
	"github.com/HK9750/venueos/internal/platform/clock"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/pricing"
	pricingpostgres "github.com/HK9750/venueos/internal/pricing/postgres"
	"github.com/HK9750/venueos/internal/publication"
	publicationpostgres "github.com/HK9750/venueos/internal/publication/postgres"
	"github.com/HK9750/venueos/internal/refund"
	refundpostgres "github.com/HK9750/venueos/internal/refund/postgres"
	"github.com/HK9750/venueos/internal/session"
	sessionpostgres "github.com/HK9750/venueos/internal/session/postgres"
	"github.com/HK9750/venueos/internal/ticket"
	ticketpostgres "github.com/HK9750/venueos/internal/ticket/postgres"
	"github.com/HK9750/venueos/internal/user"
	userpostgres "github.com/HK9750/venueos/internal/user/postgres"
	"github.com/HK9750/venueos/internal/venue"
	venuepostgres "github.com/HK9750/venueos/internal/venue/postgres"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stdout, cfg.App, cfg.Log)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.SetupTracing(ctx, cfg.App.ServiceName, cfg.App.Environment, cfg.Telemetry.OTLPEndpoint, cfg.Telemetry.TraceSampleRate)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", slog.Any("error", err))
		}
	}()

	pool, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	repository := userpostgres.New(pool)
	service := user.NewService(repository)
	transactions, err := database.NewTransactionRunner(pool, database.DefaultRetryPolicy())
	if err != nil {
		return err
	}
	apiKeyRepository := accesspostgres.New(pool, transactions)
	apiKeyService := access.NewAPIKeyService(apiKeyRepository, clock.System{})
	auditService := audit.NewService(platformpostgres.NewStore(pool))
	organizationRepository := organizationpostgres.New(pool, transactions)
	organizationService := organization.NewService(organizationRepository, clock.System{})
	membershipRepository := membershippostgres.New(pool, transactions)
	membershipService := membership.NewService(membershipRepository, clock.System{})
	invitationRepository := invitationpostgres.New(pool, transactions)
	invitationService := invitation.NewService(invitationRepository, clock.System{})
	venueRepository := venuepostgres.New(pool, transactions)
	venueService := venue.NewService(venueRepository, clock.System{})
	eventRepository := eventpostgres.New(pool, transactions)
	eventService := event.NewService(eventRepository, clock.System{})
	sessionRepository := sessionpostgres.New(pool, transactions)
	sessionService := session.NewService(sessionRepository, clock.System{})
	inventoryRepository := inventorypostgres.New(pool, transactions)
	inventoryService := inventory.NewService(inventoryRepository, clock.System{})
	entryRepository := entrypostgres.New(pool, transactions)
	ticketKeyRepository := ticketpostgres.New(pool)
	ticketVerifier, err := ticket.NewVerifier(ticketKeyRepository)
	if err != nil {
		return err
	}
	entryService := entry.NewService(entryRepository).WithCredentialVerifier(ticketVerifier)
	deviceRepository := devicepostgres.New(pool, transactions)
	deviceService := device.NewService(deviceRepository, clock.System{})
	pricingRepository := pricingpostgres.New(pool, transactions)
	pricingService := pricing.NewService(pricingRepository, clock.System{})
	channelRepository := channelpostgres.New(pool, transactions)
	channelService := channel.NewService(channelRepository, clock.System{})
	publicationRepository := publicationpostgres.New(pool, transactions)
	publicationService := publication.NewService(publicationRepository, clock.System{})
	refundRepository := refundpostgres.New(pool, transactions)
	refundService := refund.NewService(refundRepository, clock.System{})
	eventService.WithPublicationValidator(publicationService)
	server := httpapi.NewServer(service, pool, logger).
		WithOrganizations(organizationService).
		WithAPIKeys(apiKeyService).
		WithAudit(auditService).
		WithMemberships(membershipService).
		WithInvitations(invitationService).
		WithEvents(eventService).
		WithSessions(sessionService).
		WithInventory(inventoryService).
		WithEntry(entryService).
		WithDevices(deviceService).
		WithPricing(pricingService).
		WithSalesChannels(channelService).
		WithPublication(publicationService).
		WithVenues(venueService).
		WithRefunds(refundService)
	metrics := observability.NewMetrics()
	if err := metrics.RegisterDatabasePool(pool); err != nil {
		return err
	}
	bearerAuthenticator := httpapi.BearerAuthenticatorFunc(func(ctx context.Context, token string) (access.Authorization, error) {
		if len(token) >= len("vdev_") && token[:len("vdev_")] == "vdev_" {
			return deviceService.Authorization(ctx, token)
		}
		authorization, _, err := apiKeyService.Authenticate(ctx, token)
		return authorization, err
	})
	handler := httpapi.NewHandler(server, cfg.Telemetry, logger, metrics, bearerAuthenticator)
	listeners := []httpapi.Listener{{Name: "api", Config: cfg.HTTP, Handler: handler}}
	if cfg.Telemetry.MetricsEnabled || cfg.Telemetry.PprofEnabled {
		adminHTTP := cfg.HTTP
		adminHTTP.Address = cfg.Telemetry.AdminAddress
		listeners = append(listeners, httpapi.Listener{
			Name:    "admin",
			Config:  adminHTTP,
			Handler: httpapi.NewAdminHandler(cfg.Telemetry, logger, metrics),
		})
	}

	logger.Info("API starting", slog.String("version", version), slog.String("commit", commit))
	if err := httpapi.RunAll(ctx, logger, listeners...); err != nil {
		return fmt.Errorf("run API: %w", err)
	}
	return nil
}
