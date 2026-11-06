package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// InvoiceHandlerGeili serves /api/v1/admin/invoices: the issuer profile and
// commercial invoices issued by administrators.
type InvoiceHandlerGeili struct {
	invoices *service.InvoiceServiceGeili
}

func NewInvoiceHandlerGeili(invoices *service.InvoiceServiceGeili) *InvoiceHandlerGeili {
	return &InvoiceHandlerGeili{invoices: invoices}
}

// GET /api/v1/admin/invoices/issuer
func (h *InvoiceHandlerGeili) GetIssuer(c *gin.Context) {
	issuer, err := h.invoices.GetIssuer(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, issuer)
}

type invoiceIssuerRequestGeili struct {
	CompanyName    string            `json:"company_name"`
	CompanyAddress string            `json:"company_address"`
	CompanyContact string            `json:"company_contact"`
	NumberPrefix   string            `json:"number_prefix"`
	Payment        map[string]string `json:"payment"`
	FooterNote     string            `json:"footer_note"`
	SealImage      *string           `json:"seal_image"`
}

// PUT /api/v1/admin/invoices/issuer
func (h *InvoiceHandlerGeili) UpdateIssuer(c *gin.Context) {
	var req invoiceIssuerRequestGeili
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	issuer, err := h.invoices.UpdateIssuer(c.Request.Context(), service.InvoiceIssuerUpdateGeili{
		CompanyName: req.CompanyName, CompanyAddress: req.CompanyAddress, CompanyContact: req.CompanyContact,
		NumberPrefix: req.NumberPrefix, Payment: req.Payment, FooterNote: req.FooterNote, SealImage: req.SealImage,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, issuer)
}

// GET /api/v1/admin/invoices
func (h *InvoiceHandlerGeili) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.invoices.List(c.Request.Context(), pagination.PaginationParams{Page: page, PageSize: pageSize})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

type invoiceRequestGeili struct {
	IssueDate     string `json:"issue_date"`
	Currency      string `json:"currency"`
	Amount        string `json:"amount"`
	Description   string `json:"description"`
	BillToName    string `json:"bill_to_name"`
	BillToAddress string `json:"bill_to_address"`
	Notes         string `json:"notes"`
}

// POST /api/v1/admin/invoices
func (h *InvoiceHandlerGeili) Create(c *gin.Context) {
	adminID := getAdminIDFromContext(c)
	if adminID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return
	}
	var req invoiceRequestGeili
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	invoice, err := h.invoices.Create(c.Request.Context(), adminID, service.InvoiceInputGeili{
		IssueDate: req.IssueDate, Currency: req.Currency, Amount: req.Amount, Description: req.Description,
		BillToName: req.BillToName, BillToAddress: req.BillToAddress, Notes: req.Notes,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, invoice)
}

type invoiceDocumentGeili struct {
	Invoice   *service.InvoiceGeili `json:"invoice"`
	SealImage string                `json:"seal_image"`
}

// GET /api/v1/admin/invoices/:id returns the invoice with the current seal for printing.
func (h *InvoiceHandlerGeili) Get(c *gin.Context) {
	id, ok := invoiceIDParamGeili(c)
	if !ok {
		return
	}
	invoice, err := h.invoices.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	issuer, err := h.invoices.GetIssuer(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, invoiceDocumentGeili{Invoice: invoice, SealImage: issuer.SealImage})
}

// POST /api/v1/admin/invoices/:id/void
func (h *InvoiceHandlerGeili) Void(c *gin.Context) {
	id, ok := invoiceIDParamGeili(c)
	if !ok {
		return
	}
	invoice, err := h.invoices.Void(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, invoice)
}

func invoiceIDParamGeili(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid invoice ID")
		return 0, false
	}
	return id, true
}
