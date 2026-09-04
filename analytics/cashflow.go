package analytics

import (
	"strings"
	"time"

	"finam-terminal/models"
)

// FlowGroup is where one transaction lands on the Money screen.
//
// It is not the Group of the allocation breakdown: that one is a row with a
// name and a value, this one is a closed set of categories.
type FlowGroup int

const (
	// GroupDeposit and GroupWithdraw are money entering and leaving the
	// account. Their difference is the net deposit, which every return figure
	// is measured against.
	GroupDeposit FlowGroup = iota
	GroupWithdraw

	// GroupIncome is dividends and coupons received.
	GroupIncome

	// The four cost groups.
	GroupCommission
	GroupTax
	GroupLoan
	GroupFine

	// GroupTransfer is securities moving in or out. It carries a quantity, not
	// money.
	GroupTransfer

	// GroupTrade is a transaction that mirrors a deal. It is reported and
	// deliberately kept out of every money total: the realised result is
	// computed from the trades themselves, and counting the transaction too
	// would double it.
	GroupTrade

	// GroupMisc is everything else the API sends — inheritance, contract
	// termination, outcomes, others, and any category added later.
	GroupMisc

	groupCount
)

// groupLabels are the row names of the money table.
var flowGroupLabels = [groupCount]string{
	"Ввод",
	"Вывод",
	"Дивиденды и купоны",
	"Комиссии",
	"Налоги",
	"Проценты по займу",
	"Штрафы",
	"Переводы бумаг",
	"Сделки",
	"Прочее",
}

// FlowGroupOrder fixes the row order of the money table. Sorting by amount would
// make rows move between refreshes for no reason.
var FlowGroupOrder = []FlowGroup{
	GroupDeposit, GroupWithdraw, GroupIncome,
	GroupCommission, GroupTax, GroupLoan, GroupFine,
	GroupTransfer, GroupTrade, GroupMisc,
}

// Label is the row name.
func (g FlowGroup) Label() string {
	if g < 0 || g >= groupCount {
		return flowGroupLabels[GroupMisc]
	}
	return flowGroupLabels[g]
}

// categoryGroups maps the API's transaction_category enum names to groups. A
// var so a category added later can be routed without touching the algorithm.
var categoryGroups = map[string]FlowGroup{
	"DEPOSIT":              GroupDeposit,
	"WITHDRAW":             GroupWithdraw,
	"INCOME":               GroupIncome,
	"COMMISSION":           GroupCommission,
	"TAX":                  GroupTax,
	"LOAN":                 GroupLoan,
	"FINE":                 GroupFine,
	"TRANSFER":             GroupTransfer,
	"INHERITANCE":          GroupMisc,
	"CONTRACT_TERMINATION": GroupMisc,
	"OUTCOMES":             GroupMisc,
	"OTHERS":               GroupMisc,
}

// Classify decides which group a transaction belongs to.
//
// A transaction that carries a trade goes to GroupTrade whatever its category
// says. That rule comes first because it is the one that keeps the realised
// result from being counted twice — once through FIFO and once through the
// money it moved.
func Classify(t models.Transaction) FlowGroup {
	if t.Trade != nil {
		return GroupTrade
	}
	if g, ok := categoryGroups[strings.ToUpper(strings.TrimSpace(t.Category))]; ok {
		return g
	}
	return GroupMisc
}

// CurrencyFlow is the money that moved in one currency over one window.
//
// Every amount keeps the sign the API sent: withdrawals and costs are negative
// because that is the direction the money went, and a renderer that wants a
// magnitude takes it itself.
type CurrencyFlow struct {
	Currency string

	// Groups holds every group's net amount, including GroupTrade, which is
	// reported for context and excluded from the totals below.
	Groups map[FlowGroup]float64

	Deposits    float64
	Withdrawals float64 // negative
	NetDeposit  float64 // deposits + withdrawals

	// Payouts is dividends and coupons received; Costs is commissions, taxes,
	// loan interest and fines together, negative.
	Payouts float64
	Costs   float64
}

// CashFlow is the whole window, split by currency.
//
// TransferQty sits outside the currency split because a securities transfer
// moves shares, not money: filing it under a currency would suggest it belongs
// in a sum it must never enter.
type CashFlow struct {
	ByCurrency  map[string]CurrencyFlow
	TransferQty float64
}

// Flows totals the transactions of one window by group and currency.
//
// baseCurrency is where a transaction with no currency of its own is filed, for
// the same reason it is in Stats: the API does not populate the field
// consistently, and a nameless currency group is worse than the assumption.
func Flows(transactions []models.Transaction, from, to time.Time, baseCurrency string) CashFlow {
	if baseCurrency == "" {
		baseCurrency = defaultBaseCurrency
	}

	flow := CashFlow{ByCurrency: make(map[string]CurrencyFlow)}

	for _, t := range transactions {
		if !InRange(t.Timestamp, from, to) {
			continue
		}

		group := Classify(t)

		// A securities transfer contributes a quantity and nothing else. It
		// never creates a currency group, because there is no money in it.
		if group == GroupTransfer {
			flow.TransferQty += t.ChangeQty
			continue
		}

		currency := t.Currency
		if currency == "" {
			currency = baseCurrency
		}

		c, ok := flow.ByCurrency[currency]
		if !ok {
			c = CurrencyFlow{Currency: currency, Groups: make(map[FlowGroup]float64)}
		}
		c.Groups[group] += t.Amount

		switch group {
		case GroupDeposit:
			c.Deposits += t.Amount
		case GroupWithdraw:
			c.Withdrawals += t.Amount
		case GroupIncome:
			c.Payouts += t.Amount
		case GroupCommission, GroupTax, GroupLoan, GroupFine:
			c.Costs += t.Amount
		case GroupTrade:
			// Reported in Groups above and kept out of every total here.
		}

		c.NetDeposit = c.Deposits + c.Withdrawals
		flow.ByCurrency[currency] = c
	}

	return flow
}
