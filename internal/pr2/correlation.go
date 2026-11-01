package pr2

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

type Order struct {
	ID       int64    `json:"order_id"`
	Products []string `json:"products"`
}

type Pair [2]string

type Record struct {
	Key    string
	Counts map[string]int64
	Pair   Pair
	Count  int64
}

func CheckAlgorithm(algorithm string) error {
	if algorithm != "pairs" && algorithm != "stripes" {
		return fmt.Errorf("algorithm must be pairs or stripes")
	}
	return nil
}

// Each input record occupies one line. Raise the limit for large baskets/stripes.
func Lines(r io.Reader, consume func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if err := consume(scanner.Text()); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("after line %d: %w", line, err)
	}
	return nil
}

func validID(id string) bool {
	return strings.TrimSpace(id) != "" && !strings.ContainsAny(id, "\t\r\n")
}

func Map(algorithm string, in io.Reader, out io.Writer) error {
	if err := CheckAlgorithm(algorithm); err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	err := Lines(in, func(line string) error {
		var order Order
		if err := json.Unmarshal([]byte(line), &order); err != nil {
			return fmt.Errorf("order: %w", err)
		}
		seen := make(map[string]bool)
		products := make([]string, 0, len(order.Products))
		for _, id := range order.Products {
			if !validID(id) {
				return fmt.Errorf("invalid product ID %q", id)
			}
			if !seen[id] {
				seen[id] = true
				products = append(products, id)
			}
		}
		sort.Strings(products)
		for _, a := range products {
			stripe := make(map[string]int64)
			for _, b := range products {
				if a == b {
					continue
				}
				if algorithm == "pairs" {
					key, _ := json.Marshal(Pair{a, b})
					if _, err := fmt.Fprintf(w, "%s\t1\n", key); err != nil {
						return err
					}
				} else {
					stripe[b] = 1
				}
			}
			if algorithm == "stripes" && len(stripe) > 0 {
				data, _ := json.Marshal(stripe)
				if _, err := fmt.Fprintf(w, "%s\t%s\n", a, data); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return w.Flush()
}

// ParseRecord is shared by reducers and readers, so malformed output fails explicitly.
func ParseRecord(algorithm, line string) (Record, error) {
	key, value, ok := strings.Cut(line, "\t")
	if !ok || key == "" {
		return Record{}, fmt.Errorf("expected key<TAB>value")
	}
	rec := Record{Key: key}
	switch algorithm {
	case "pairs":
		var ids []string
		if err := json.Unmarshal([]byte(key), &ids); err != nil || len(ids) != 2 {
			return rec, fmt.Errorf("invalid pair key %q", key)
		}
		if !validID(ids[0]) || !validID(ids[1]) || ids[0] == ids[1] {
			return rec, fmt.Errorf("invalid pair %q", key)
		}
		canonical, _ := json.Marshal(Pair{ids[0], ids[1]})
		if key != string(canonical) {
			return rec, fmt.Errorf("pair key must use compact JSON encoding")
		}
		rec.Pair = Pair{ids[0], ids[1]}
		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil || count <= 0 {
			return rec, fmt.Errorf("invalid positive count %q", value)
		}
		rec.Count = count
	case "stripes":
		if !validID(key) {
			return rec, fmt.Errorf("invalid product ID %q", key)
		}
		if err := json.Unmarshal([]byte(value), &rec.Counts); err != nil || len(rec.Counts) == 0 {
			return rec, fmt.Errorf("invalid or empty stripe")
		}
		for id, count := range rec.Counts {
			if !validID(id) || id == key || count <= 0 {
				return rec, fmt.Errorf("invalid stripe entry %q: %d", id, count)
			}
		}
	default:
		return rec, CheckAlgorithm(algorithm)
	}
	return rec, nil
}

func Add(a, b int64) (int64, error) {
	if b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("count overflow")
	}
	return a + b, nil
}

// Reduce also serves as a combiner: addition is associative and formats are unchanged.
func Reduce(algorithm string, in io.Reader, out io.Writer) error {
	if err := CheckAlgorithm(algorithm); err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	var key string
	var sum int64
	var stripe map[string]int64
	flush := func() error {
		if key == "" {
			return nil
		}
		if algorithm == "pairs" {
			_, err := fmt.Fprintf(w, "%s\t%d\n", key, sum)
			return err
		}
		data, err := json.Marshal(stripe)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\t%s\n", key, data)
		return err
	}
	err := Lines(in, func(line string) error {
		rec, err := ParseRecord(algorithm, line)
		if err != nil {
			return err
		}
		if key != "" && rec.Key < key {
			return fmt.Errorf("input is not sorted by key")
		}
		if rec.Key != key {
			if err := flush(); err != nil {
				return err
			}
			key = rec.Key
			sum = 0
			stripe = make(map[string]int64)
		}
		if algorithm == "pairs" {
			sum, err = Add(sum, rec.Count)
			return err
		}
		for id, count := range rec.Counts {
			stripe[id], err = Add(stripe[id], count)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return w.Flush()
}
