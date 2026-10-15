package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SupplierHandlerGeili serves /api/v1/supplier/*: a supplier manages only accounts
// it owns and reads their usage without customer identity or customer charges.
type SupplierHandlerGeili struct {
	supplier    *service.SupplierAccountServiceGeili
	accountTest *service.AccountTestService
}

func NewSupplierHandlerGeili(supplier *service.SupplierAccountServiceGeili, accounts *AccountHandler) *SupplierHandlerGeili {
	h := &SupplierHandlerGeili{supplier: supplier}
	if accounts != nil {
		h.accountTest = accounts.accountTestService
	}
	return h
}

type SupplierAccountGeili struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Notes          *string    `json:"notes"`
	Platform       string     `json:"platform"`
	Type           string     `json:"type"`
	BaseURL        string     `json:"base_url"`
	APIKeyHint     string     `json:"api_key_hint"`
	Concurrency    int        `json:"concurrency"`
	RateMultiplier float64    `json:"rate_multiplier"`
	Status         string     `json:"status"`
	Schedulable    bool       `json:"schedulable"`
	ReviewState    string     `json:"review_state"`
	ErrorMessage   string     `json:"error_message"`
	Groups         []string   `json:"groups"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// supplierReviewStateGeili folds status/schedulable into what the supplier can act on.
func supplierReviewStateGeili(a *service.Account) string {
	switch {
	case a.Status == service.StatusError:
		return "error"
	case a.Status == service.StatusActive && a.Schedulable:
		return "running"
	case a.Status == service.StatusActive:
		return "paused"
	case a.Status == service.StatusDisabled:
		return "disabled"
	default:
		return "pending"
	}
}

func supplierKeyHintGeili(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func supplierAccountFromServiceGeili(a *service.Account) SupplierAccountGeili {
	out := SupplierAccountGeili{
		ID:             a.ID,
		Name:           a.Name,
		Notes:          a.Notes,
		Platform:       a.Platform,
		Type:           a.Type,
		Concurrency:    a.Concurrency,
		RateMultiplier: a.BillingRateMultiplier(),
		Status:         a.Status,
		Schedulable:    a.Schedulable,
		ReviewState:    supplierReviewStateGeili(a),
		Groups:         []string{},
		LastUsedAt:     a.LastUsedAt,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
	if a.ErrorMessage != "" {
		out.ErrorMessage = a.ErrorMessage
	}
	if a.Type == service.AccountTypeAPIKey {
		out.BaseURL, _ = a.Credentials["base_url"].(string)
		key, _ := a.Credentials["api_key"].(string)
		out.APIKeyHint = supplierKeyHintGeili(key)
	}
	for _, g := range a.Groups {
		if g != nil {
			out.Groups = append(out.Groups, g.Name)
		}
	}
	return out
}

func supplierIDGeili(c *gin.Context) (int64, bool) {
	id := getAdminIDFromContext(c)
	if id <= 0 {
		response.Unauthorized(c, "User not found in context")
		return 0, false
	}
	return id, true
}

func supplierAccountIDParamGeili(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

// GET /api/v1/supplier/meta
func (h *SupplierHandlerGeili) Meta(c *gin.Context) {
	response.Success(c, gin.H{"platforms": service.SupplierPlatformsGeili})
}

// GET /api/v1/supplier/accounts
func (h *SupplierHandlerGeili) ListAccounts(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	accounts, total, err := h.supplier.List(c.Request.Context(), ownerID, page, pageSize, c.Query("platform"), c.Query("status"), c.Query("search"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]SupplierAccountGeili, 0, len(accounts))
	for i := range accounts {
		out = append(out, supplierAccountFromServiceGeili(&accounts[i]))
	}
	response.Paginated(c, out, total, page, pageSize)
}

type supplierAccountRequestGeili struct {
	Platform    string  `json:"platform"`
	Name        string  `json:"name"`
	Notes       *string `json:"notes"`
	APIKey      string  `json:"api_key"`
	BaseURL     *string `json:"base_url"`
	Concurrency *int    `json:"concurrency"`
}

func (r supplierAccountRequestGeili) input() service.SupplierAccountInputGeili {
	return service.SupplierAccountInputGeili{
		Platform: strings.TrimSpace(r.Platform), Name: r.Name, Notes: r.Notes,
		APIKey: r.APIKey, BaseURL: r.BaseURL, Concurrency: r.Concurrency,
	}
}

// POST /api/v1/supplier/accounts
func (h *SupplierHandlerGeili) CreateAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	var req supplierAccountRequestGeili
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.supplier.Create(c.Request.Context(), ownerID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, supplierAccountFromServiceGeili(account))
}

// GET /api/v1/supplier/accounts/:id
func (h *SupplierHandlerGeili) GetAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	account, err := h.supplier.Get(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, supplierAccountFromServiceGeili(account))
}

// PUT /api/v1/supplier/accounts/:id
func (h *SupplierHandlerGeili) UpdateAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	var req supplierAccountRequestGeili
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.supplier.Update(c.Request.Context(), ownerID, accountID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, supplierAccountFromServiceGeili(account))
}

// POST /api/v1/supplier/accounts/:id/pause
func (h *SupplierHandlerGeili) PauseAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	account, err := h.supplier.Pause(c.Request.Context(), ownerID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, supplierAccountFromServiceGeili(account))
}

// DELETE /api/v1/supplier/accounts/:id
func (h *SupplierHandlerGeili) DeleteAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	if err := h.supplier.Delete(c.Request.Context(), ownerID, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account deleted successfully"})
}

type supplierTestRequestGeili struct {
	ModelID string `json:"model_id"`
}

// POST /api/v1/supplier/accounts/:id/test streams the same SSE events as the admin test.
// Unlike the admin path, a successful test never clears error or rate-limit state.
func (h *SupplierHandlerGeili) TestAccount(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	if _, err := h.supplier.Get(c.Request.Context(), ownerID, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.accountTest == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account test unavailable")
		return
	}
	var req supplierTestRequestGeili
	_ = c.ShouldBindJSON(&req)
	_ = h.accountTest.TestAccountConnection(c, accountID, strings.TrimSpace(req.ModelID), "", "")
}

// SupplierUsageLogGeili omits user, API key, IP, user agent and customer charges.
type SupplierUsageLogGeili struct {
	ID                  int64     `json:"id"`
	AccountID           int64     `json:"account_id"`
	AccountName         string    `json:"account_name"`
	Model               string    `json:"model"`
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	ImageCount          int       `json:"image_count"`
	AccountCost         float64   `json:"account_cost"`
	Stream              bool      `json:"stream"`
	DurationMs          *int      `json:"duration_ms"`
	FirstTokenMs        *int      `json:"first_token_ms"`
	CreatedAt           time.Time `json:"created_at"`
}

func supplierUsageFromServiceGeili(l *service.UsageLog) SupplierUsageLogGeili {
	model := l.RequestedModel
	if model == "" {
		model = l.Model
	}
	base := l.TotalCost
	if l.AccountStatsCost != nil {
		base = *l.AccountStatsCost
	}
	multiplier := 1.0
	if l.AccountRateMultiplier != nil {
		multiplier = *l.AccountRateMultiplier
	}
	out := SupplierUsageLogGeili{
		ID: l.ID, AccountID: l.AccountID, Model: model,
		InputTokens: l.InputTokens, OutputTokens: l.OutputTokens,
		CacheCreationTokens: l.CacheCreationTokens, CacheReadTokens: l.CacheReadTokens,
		ImageCount: l.ImageCount, AccountCost: base * multiplier,
		Stream: l.EffectiveRequestType() == service.RequestTypeStream, DurationMs: l.DurationMs,
		FirstTokenMs: l.FirstTokenMs, CreatedAt: l.CreatedAt,
	}
	if l.Account != nil {
		out.AccountName = l.Account.Name
	}
	return out
}

const supplierUsageMaxDaysGeili = 93

// supplierUsageFiltersGeili accepts only account, model and date filters. Everything
// customer-related is dropped again by the service scope.
func supplierUsageFiltersGeili(c *gin.Context) (usagestats.UsageLogFilters, bool) {
	filters := usagestats.UsageLogFilters{ModelFilterSource: usagestats.ModelSourceRequested}
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid account_id")
			return filters, false
		}
		filters.AccountID = id
	}
	filters.Model = strings.TrimSpace(c.Query("model"))
	userTZ := c.Query("timezone")
	now := timezone.NowInUserLocation(userTZ)
	end := timezone.StartOfDayInUserLocation(now, userTZ).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -7)
	if raw := c.Query("start_date"); raw != "" {
		t, err := timezone.ParseInUserLocation("2006-01-02", raw, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return filters, false
		}
		start = t
	}
	if raw := c.Query("end_date"); raw != "" {
		t, err := timezone.ParseInUserLocation("2006-01-02", raw, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return filters, false
		}
		end = t.AddDate(0, 0, 1)
	}
	if !end.After(start) || end.Sub(start) > supplierUsageMaxDaysGeili*24*time.Hour+time.Hour {
		response.BadRequest(c, "Invalid date range (max 93 days)")
		return filters, false
	}
	filters.StartTime, filters.EndTime = &start, &end
	return filters, true
}

// GET /api/v1/supplier/usage
func (h *SupplierHandlerGeili) ListUsage(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	filters, ok := supplierUsageFiltersGeili(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: "created_at", SortOrder: "desc"}
	records, result, err := h.supplier.ListUsage(c.Request.Context(), ownerID, params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]SupplierUsageLogGeili, 0, len(records))
	for i := range records {
		out = append(out, supplierUsageFromServiceGeili(&records[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GET /api/v1/supplier/usage/stats
func (h *SupplierHandlerGeili) UsageStats(c *gin.Context) {
	ownerID, ok := supplierIDGeili(c)
	if !ok {
		return
	}
	filters, ok := supplierUsageFiltersGeili(c)
	if !ok {
		return
	}
	stats, err := h.supplier.UsageStats(c.Request.Context(), ownerID, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accountCost := 0.0
	if stats.TotalAccountCost != nil {
		accountCost = *stats.TotalAccountCost
	}
	response.Success(c, gin.H{
		"total_requests":              stats.TotalRequests,
		"total_input_tokens":          stats.TotalInputTokens,
		"total_output_tokens":         stats.TotalOutputTokens,
		"total_cache_creation_tokens": stats.TotalCacheCreationTokens,
		"total_cache_read_tokens":     stats.TotalCacheReadTokens,
		"total_tokens":                stats.TotalTokens,
		"total_account_cost":          accountCost,
		"average_duration_ms":         stats.AverageDurationMs,
	})
}

type supplierOwnerRequestGeili struct {
	OwnerUserID int64 `json:"owner_user_id"`
}

// PUT /api/v1/admin/accounts/:id/supplier-owner assigns or clears (0) the supplier owner.
func (h *SupplierHandlerGeili) AdminSetOwner(c *gin.Context) {
	accountID, ok := supplierAccountIDParamGeili(c)
	if !ok {
		return
	}
	var req supplierOwnerRequestGeili
	if err := c.ShouldBindJSON(&req); err != nil || req.OwnerUserID < 0 {
		response.BadRequest(c, "Invalid request")
		return
	}
	if err := h.supplier.SetOwner(c.Request.Context(), accountID, req.OwnerUserID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	owners, err := h.supplier.Owners(c.Request.Context(), []int64{accountID})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	owner, found := owners[accountID]
	if !found {
		response.Success(c, gin.H{"supplier_owner": nil})
		return
	}
	response.Success(c, gin.H{"supplier_owner": owner})
}
