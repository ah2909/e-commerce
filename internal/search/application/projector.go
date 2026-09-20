package application

import (
	"context"

	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type Resolver interface {
	ResolveSearchProduct(context.Context, ids.StoreID, ids.ProductID) (catalogapplication.SearchProduct, bool, error)
}
