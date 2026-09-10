package analytics

import "finam-terminal/models"

// Level is how alarming a risk figure is, translated to a colour by the
// renderer.
type Level int

const (
	LevelGood Level = iota
	LevelWarn
	LevelBad
)

// Thresholds for the two coloured figures. Margin utilisation reads upwards —
// the more of the account's equity is committed as margin, the worse — while
// the cushion above the maintenance level reads downwards, so its bands are
// mirrored.
const (
	utilizationWarn = 0.5
	utilizationBad  = 0.8

	cushionWarn = 0.5
	cushionBad  = 0.2
)

// Metric is a computed number that may not exist. Valid is false when the
// account does not report the inputs — an MCT portfolio, an absent oneof, or
// equity of zero — and the renderer then prints "Н/Д" instead of a figure.
// Value is always finite, so a caller that ignores Valid still cannot print
// NaN.
type Metric struct {
	Value float64
	Valid bool
}

// RiskInput is the account's own margin report plus the exposure the
// allocation already computed.
type RiskInput struct {
	Account models.AccountInfo

	// GrossExposure is the sum of position values by magnitude, converted into
	// the base currency — Allocation.Exposure, which leaves every kind of cash
	// out, bought currency included. Leverage compares it to equity.
	GrossExposure float64
}

// Risk is the margin and risk block of the overview.
type Risk struct {
	Kind          string // "MC", "MCT", "FORTS", or empty
	HasMarginData bool

	Equity      float64
	EquityValid bool

	// Utilization is committed margin over equity: initial_margin for a margin
	// account, money_reserved for a derivatives one.
	Utilization Metric

	// Cushion is how far equity sits above the maintenance margin, as a share
	// of equity — the distance to a margin call. Only a margin account reports
	// a maintenance level, so FORTS leaves this invalid rather than guessing.
	Cushion Metric

	// Leverage is gross position exposure over equity.
	Leverage Metric
}

// RiskMetrics computes the margin block.
//
// Everything here divides by equity, so equity is checked once up front: a
// zero, negative or unreadable equity makes every ratio meaningless, and they
// all come back invalid together rather than each finding out on its own.
func RiskMetrics(in RiskInput) Risk {
	account := in.Account

	risk := Risk{
		Kind:          account.PortfolioKind,
		HasMarginData: account.HasMarginData,
	}

	equity, ok := ParseNumber(account.Equity)
	risk.Equity = equity
	risk.EquityValid = ok && equity > 0

	if !risk.EquityValid || !account.HasMarginData {
		return risk
	}

	switch account.PortfolioKind {
	case "MC":
		risk.Utilization = Metric{Value: account.InitialMargin / equity, Valid: true}
		risk.Cushion = Metric{Value: (equity - account.MaintenanceMargin) / equity, Valid: true}
		risk.Leverage = Metric{Value: in.GrossExposure / equity, Valid: true}
	case "FORTS":
		// The derivatives portfolio reports reserved collateral and nothing
		// else: there is no maintenance level to measure a cushion against,
		// and no basis for a leverage figure the user could trust.
		risk.Utilization = Metric{Value: account.MoneyReserved / equity, Valid: true}
	}

	return risk
}

// UtilizationLevel colours margin use: comfortable below half, a warning up to
// four fifths, and alarming beyond that.
func UtilizationLevel(value float64) Level {
	switch {
	case value >= utilizationBad:
		return LevelBad
	case value >= utilizationWarn:
		return LevelWarn
	default:
		return LevelGood
	}
}

// CushionLevel colours the distance to a margin call. The scale is mirrored:
// here a large number is the safe one.
func CushionLevel(value float64) Level {
	// The comparisons mirror UtilizationLevel exactly, boundaries included: a
	// figure sitting on a threshold is shown in the more cautious of the two
	// colours, on both scales.
	switch {
	case value <= cushionBad:
		return LevelBad
	case value <= cushionWarn:
		return LevelWarn
	default:
		return LevelGood
	}
}
