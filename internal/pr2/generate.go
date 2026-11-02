package pr2

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
)

type Product struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GeneratorConfig struct {
	Orders   int
	Products int
	MinItems int
	MaxItems int
	Seed     int64
}

// Names are grouped in fives to make correlated baskets visible in recommendations.
var names = []string{
	"Кофе", "Молоко", "Сахар", "Печенье", "Шоколад",
	"Макароны", "Томатный соус", "Сыр", "Оливковое масло", "Базилик",
	"Чай", "Мёд", "Лимон", "Пряники", "Варенье",
	"Шампунь", "Кондиционер", "Мыло", "Гель для душа", "Зубная паста",
	"Тетрадь", "Ручка", "Карандаш", "Ластик", "Линейка",
	"Хлеб", "Масло", "Яйца", "Ветчина", "Йогурт",
}

func Generate(c GeneratorConfig, catalogOut, ordersOut io.Writer) error {
	products := make([]Product, c.Products)
	for i := range products {
		name := fmt.Sprintf("Товар %06d", i+1)
		if i < len(names) {
			name = names[i]
		}
		products[i] = Product{ID: fmt.Sprintf("p%06d", i+1), Name: name}
	}
	if err := json.NewEncoder(catalogOut).Encode(products); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(c.Seed))
	enc := json.NewEncoder(ordersOut)
	for n := 0; n < c.Orders; n++ {
		size := c.MinItems + rng.Intn(c.MaxItems-c.MinItems+1)
		anchor := rng.Intn(c.Products)
		groupStart := anchor / 5 * 5
		groupEnd := groupStart + 5
		if groupEnd > c.Products {
			groupEnd = c.Products
		}
		chosen := map[int]bool{anchor: true}
		ids := []string{products[anchor].ID}
		// Finite candidate lists avoid rejection loops when a basket is nearly full.
		group := make([]int, 0, 5)
		rest := make([]int, 0, c.Products)
		for i := range products {
			if i == anchor {
				continue
			}
			if i >= groupStart && i < groupEnd {
				group = append(group, i)
			} else {
				rest = append(rest, i)
			}
		}
		rng.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
		rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
		for len(ids) < size {
			pool := &rest
			if len(group) > 0 && (len(rest) == 0 || rng.Float64() < 0.8) {
				pool = &group
			}
			index := (*pool)[len(*pool)-1]
			*pool = (*pool)[:len(*pool)-1]
			if !chosen[index] {
				chosen[index] = true
				ids = append(ids, products[index].ID)
			}
		}
		if err := enc.Encode(Order{ID: int64(n + 1), Products: ids}); err != nil {
			return err
		}
	}
	return nil
}
