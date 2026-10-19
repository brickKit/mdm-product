// Command mdm-product runs the product component alone (serve, migrate); the shells compile in product.Definition().
package main

import (
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/mdm-product/v3/product"
)

func main() { launcher.Main(product.Definition()) }
