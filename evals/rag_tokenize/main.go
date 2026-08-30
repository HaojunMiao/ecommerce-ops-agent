// Command rag_tokenize exposes the production GSE tokenizer to evaluation scripts.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
)

func main() {
	var texts []string
	if err := json.NewDecoder(os.Stdin).Decode(&texts); err != nil {
		fmt.Fprintln(os.Stderr, "decode texts:", err)
		os.Exit(1)
	}
	out := make([][]string, len(texts))
	for i, value := range texts {
		out[i] = retriever.LexicalTokens(value)
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode tokens:", err)
		os.Exit(1)
	}
}
