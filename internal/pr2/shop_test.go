package pr2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SkyGreenxd/bigdata/pkg/webhdfs"
)

func sorted(input string) string {
	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	if input == "" {
		return ""
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func pipeline(t *testing.T, algorithm, input string, combine bool) map[Pair]int64 {
	t.Helper()
	// Separate mapper chunks and three reducer partitions exercise shuffle boundaries.
	chunks := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	partitions := make([]strings.Builder, 3)
	for start := 0; start < len(chunks); start += 7 {
		end := start + 7
		if end > len(chunks) {
			end = len(chunks)
		}
		var mapped bytes.Buffer
		if err := Map(algorithm, strings.NewReader(strings.Join(chunks[start:end], "\n")), &mapped); err != nil {
			t.Fatal(err)
		}
		intermediate := sorted(mapped.String())
		if combine {
			var combined bytes.Buffer
			if err := Reduce(algorithm, strings.NewReader(intermediate), &combined); err != nil {
				t.Fatal(err)
			}
			intermediate = combined.String()
		}
		if err := Lines(strings.NewReader(intermediate), func(line string) error {
			key, _, _ := strings.Cut(line, "\t")
			hash := fnv.New32a()
			hash.Write([]byte(key))
			fmt.Fprintln(&partitions[hash.Sum32()%3], line)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	result := make(map[Pair]int64)
	for i := range partitions {
		var reduced bytes.Buffer
		if err := Reduce(algorithm, strings.NewReader(sorted(partitions[i].String())), &reduced); err != nil {
			t.Fatal(err)
		}
		if err := Lines(&reduced, func(line string) error {
			rec, err := ParseRecord(algorithm, line)
			if err != nil {
				return err
			}
			if algorithm == "pairs" {
				result[rec.Pair] = rec.Count
			} else {
				for id, count := range rec.Counts {
					result[Pair{rec.Key, id}] = count
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestKnownCounts(t *testing.T) {
	input, err := os.ReadFile("testdata/orders.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[Pair]int64{{"A", "B"}: 2, {"B", "A"}: 2, {"A", "C"}: 2, {"C", "A"}: 2, {"B", "C"}: 1, {"C", "B"}: 1}
	for _, algorithm := range []string{"pairs", "stripes"} {
		for _, combine := range []bool{false, true} {
			actual := pipeline(t, algorithm, string(input), combine)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("%s combine=%v: %v", algorithm, combine, actual)
			}
		}
	}
}

func TestRandomCountsAgainstOrderMembership(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	var input bytes.Buffer
	expected := make(map[Pair]int64)
	ids := []string{"A", "B", "C", "Кофе", "id,with,comma", "id\"quote", "<tag>"}
	for n := 0; n < 150; n++ {
		order := Order{ID: int64(n)}
		// Repeated IDs deliberately model multiple units of the same product.
		for k := rng.Intn(12); k > 0; k-- {
			order.Products = append(order.Products, ids[rng.Intn(len(ids))])
		}
		json.NewEncoder(&input).Encode(order)
		for _, a := range ids {
			for _, b := range ids {
				if a != b && contains(order.Products, a) && contains(order.Products, b) {
					expected[Pair{a, b}]++
				}
			}
		}
	}
	for _, algorithm := range []string{"pairs", "stripes"} {
		for _, combine := range []bool{false, true} {
			if actual := pipeline(t, algorithm, input.String(), combine); !reflect.DeepEqual(actual, expected) {
				t.Fatalf("%s combine=%v differs", algorithm, combine)
			}
		}
	}
}
func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct{ algorithm, input string }{
		{"pairs", "[\"A\",\"A\"]\t1\n"},
		{"pairs", "[\"A\",\"B\"]\t0\n"},
		{"pairs", "[\"A\",\"B\"]\t9223372036854775807\n[\"A\",\"B\"]\t1\n"},
		{"pairs", "[\"B\",\"C\"]\t1\n[\"A\",\"B\"]\t1\n"},
		{"stripes", "A\t{\"B\":-1}\n"},
		{"stripes", "A\t{\"A\":1}\n"},
		{"stripes", "A\t{\"B\":9223372036854775807}\nA\t{\"B\":1}\n"},
		{"stripes", "A\t{}\n"},
	}
	for _, c := range cases {
		if err := Reduce(c.algorithm, strings.NewReader(c.input), io.Discard); err == nil {
			t.Errorf("accepted %q", c.input)
		}
	}
	if err := Map("pairs", strings.NewReader(`{"products":["bad\tID"]}`), io.Discard); err == nil {
		t.Fatal("accepted bad ID")
	}
}

func TestGenerate(t *testing.T) {
	for _, c := range []GeneratorConfig{
		{Orders: 100, Products: 30, MinItems: 1, MaxItems: 30, Seed: 3},
		{Orders: 10, Products: 1, MinItems: 1, MaxItems: 1, Seed: 4},
		{Orders: 0, Products: 2, MinItems: 1, MaxItems: 2, Seed: 4},
	} {
		var catalog, orders, catalog2, orders2 bytes.Buffer
		if err := Generate(c, &catalog, &orders); err != nil {
			t.Fatal(err)
		}
		if err := Generate(c, &catalog2, &orders2); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(catalog.Bytes(), catalog2.Bytes()) || !bytes.Equal(orders.Bytes(), orders2.Bytes()) {
			t.Fatal("not reproducible")
		}
		products, err := ReadCatalog(&catalog)
		if err != nil {
			t.Fatal(err)
		}
		known := make(map[string]bool)
		for _, p := range products {
			known[p.ID] = true
		}
		n := 0
		if err := Lines(&orders, func(line string) error {
			n++
			var o Order
			if err := json.Unmarshal([]byte(line), &o); err != nil {
				return err
			}
			if len(o.Products) < c.MinItems || len(o.Products) > c.MaxItems {
				t.Fatal("basket size out of range")
			}
			seen := make(map[string]bool)
			for _, id := range o.Products {
				if seen[id] || !known[id] {
					t.Fatal("invalid basket")
				}
				seen[id] = true
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if n != c.Orders {
			t.Fatal("wrong order count")
		}
	}
	if err := Generate(GeneratorConfig{Orders: 1, Products: 1, MinItems: 1, MaxItems: 2}, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted impossible basket")
	}
}

type memoryFS map[string]string

func (fs memoryFS) List(dir string) ([]webhdfs.FileStatus, error) {
	var files []webhdfs.FileStatus
	prefix := dir + "/"
	for filename := range fs {
		if strings.HasPrefix(filename, prefix) {
			files = append(files, webhdfs.FileStatus{PathSuffix: strings.TrimPrefix(filename, prefix), Type: "FILE"})
		}
	}
	return files, nil
}
func (fs memoryFS) OpenReader(filename string) (io.ReadCloser, error) {
	data, ok := fs[filename]
	if !ok {
		return nil, fmt.Errorf("missing file %s", filename)
	}
	return io.NopCloser(strings.NewReader(data)), nil
}

func TestAdvisorMultiplePartsTop10(t *testing.T) {
	catalog := []Product{{ID: "A", Name: "Кофе"}}
	fs := memoryFS{"/pairs/_SUCCESS": "", "/stripes/_SUCCESS": "", "/pairs/_temporary": "invalid ignored data"}
	counts := make(map[string]int64)
	for n := 1; n <= 12; n++ {
		id := fmt.Sprintf("p%02d", n)
		catalog = append(catalog, Product{ID: id, Name: fmt.Sprintf("Товар %02d", n)})
		count := int64(n/2 + 1)
		counts[id] = count
		fs[fmt.Sprintf("/pairs/part-%05d", n%3)] += fmt.Sprintf("[\"A\",%q]\t%d\n", id, count)
	}
	stripe, _ := json.Marshal(counts)
	fs["/stripes/part-00000"] = "A\t" + string(stripe) + "\n"
	fs["/stripes/part-00001"] = ""
	var first []Recommendation
	for _, algorithm := range []string{"pairs", "stripes"} {
		result, err := Recommend(fs, "/"+algorithm, algorithm, catalog, "кофе", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 10 || result[0].Product.ID != "p12" || result[1].Product.ID != "p10" || result[2].Product.ID != "p11" {
			t.Fatalf("wrong top 10: %v", result)
		}
		if first == nil {
			first = result
		} else if !reflect.DeepEqual(first, result) {
			t.Fatal("advisors differ")
		}
	}
	if total, err := Compare(fs, "/pairs", "/stripes"); err != nil || total != 12 {
		t.Fatalf("compare: %d %v", total, err)
	}
	fs["/stripes/part-00000"] = "A\t{\"p01\":99}\n"
	if _, err := Compare(fs, "/pairs", "/stripes"); err == nil {
		t.Fatal("accepted mismatch")
	}
	if _, err := Recommend(fs, "/pairs", "pairs", catalog, "unknown", 10); err == nil {
		t.Fatal("unknown product accepted")
	}
	empty, err := Recommend(fs, "/pairs", "pairs", catalog, "Товар 01", 10)
	if err != nil || len(empty) != 0 {
		t.Fatal("expected no recommendations")
	}
	delete(fs, "/pairs/_SUCCESS")
	if _, err := Recommend(fs, "/pairs", "pairs", catalog, "Кофе", 10); err == nil {
		t.Fatal("accepted incomplete job")
	}
}
