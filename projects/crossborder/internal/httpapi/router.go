package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/HaojunMiao/ecommerce-ops-agent/projects/crossborder/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/projects/crossborder/internal/service"
)

type Service interface {
	GetOrder(id string) (domain.Order, bool)
	Inventory(sku string) []domain.InventoryBalance
	CreateTransfer(req service.TransferRequest) (domain.InventoryTransfer, error)
}

func New(svc Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 注册业务相关接口

	// 这里的接口方法怎么和service.go中的方法联系和绑定: cmd/main.go里面会调用service.NewSeeded()
	// 即实现了接口

	// 1. 查订单  GET /api/orders/ORD-123
	mux.HandleFunc("GET /api/orders/{orderID}", func(w http.ResponseWriter, r *http.Request) {
		order, ok := svc.GetOrder(strings.TrimSpace(r.PathValue("orderID")))
		if !ok {
			// 不存在这个OrderID
			writeError(w, http.StatusNotFound, "order_not_found")
			return
		}
		writeJSON(w, http.StatusOK, order)
	})

	// 2. 查库存  GET /api/inventory?sku=xxx
	mux.HandleFunc("GET /api/inventory", func(w http.ResponseWriter, r *http.Request) {
		// query参数传sku
		sku := strings.TrimSpace(r.URL.Query().Get("sku"))
		if len(sku) == 0 {
			writeError(w, http.StatusBadRequest, "sku_is_required")
			return
		}

		data := svc.Inventory(sku)
		writeJSON(w, http.StatusOK, data)
	})

	// 3. 创建仓库调拨单
	// 会改变库存数据，即POST请求
	mux.HandleFunc("POST /api/transfers", func(w http.ResponseWriter, r *http.Request) {
		// 解析请求
		var req service.TransferRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json")
			return
		}

		transfer, err := svc.CreateTransfer(req)
		if err != nil {
			// 判断是哪种错误
			status := http.StatusConflict
			if errors.Is(err, service.ErrIdempotencyKey) || errors.Is(err, service.ErrInvalidTransition) {
				status = http.StatusBadRequest
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, transfer)
	})

	return mux
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

// writeJSON 写响应的方法
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
