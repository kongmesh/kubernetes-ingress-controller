package rootcmd

import (
	"context"
	"fmt"
	"io"
	"os/signal"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kong/kubernetes-ingress-controller/v3/internal/manager"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/telemetries"
)

// Run sets up a default stderr logger and starts the controller manager.
func Run(ctx context.Context, c *manager.Config, output io.Writer) error {
	logger, err := manager.SetupLoggers(c, output)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	ctx = ctrl.LoggerInto(ctx, logger)

	ctx, err = SetupSignalHandler(ctx, c, logger)
	if err != nil {
		return fmt.Errorf("failed to setup signal handler: %w", err)
	}
	defer signal.Ignore(shutdownSignals...)

	return RunWithLogger(ctx, c, logger)
}

// RunWithLogger starts the controller manager with a provided logger.
func RunWithLogger(ctx context.Context, c *manager.Config, logger logr.Logger) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}

	exp, err := telemetries.NewExporter(ctx)
	if err != nil {
		return fmt.Errorf("failed to create the OTel exporter: %w", err)
	}

	// Create a new tracer provider with the given exporter.
	tp := telemetries.NewTraceProvider(exp)

	// Handle shutdown properly so nothing leaks.
	defer func() { _ = tp.Shutdown(ctx) }()

	// Set the global trace provider.
	otel.SetTracerProvider(tp)

	diag, err := StartDiagnosticsServer(ctx, c.DiagnosticServerPort, c, logger)
	if err != nil {
		return fmt.Errorf("failed to start diagnostics server: %w", err)
	}

	_, span := tp.Tracer("internal/cmd").Start(ctx, "manager.Run")
	defer span.End()

	return manager.Run(ctx, c, diag.ConfigDumps, logger)
}
