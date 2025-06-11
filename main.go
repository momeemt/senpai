package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env/v11"
	"google.golang.org/genai"
)

type config struct {
	GeminiApiKey string `env:"GEMINI_API_KEY"`
}

func getAllNotes() (string, error) {
	var b bytes.Buffer
	root := "src"

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		if filepath.Base(path) == "SUMMARY.md" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "\n\n---\nPath: %s\n\n%s\n", path, content)
		return nil
	})
	if err != nil {
		return "", err
	}

	return b.String(), nil
}

func buildSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title":      {Type: genai.TypeString},
			"correction": {Type: genai.TypeString},
			"next":       {Type: genai.TypeString},
			"advanced":   {Type: genai.TypeString},
		},
		Required:         []string{"title", "correction", "next", "advanced"},
		PropertyOrdering: []string{"title", "correction", "next", "advanced"},
	}
}

func getExistingTitles() ([]string, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--state", "all",
		"--json", "title",
		"--limit", "1000",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var rows []struct{ Title string }
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	var titles []string
	for _, r := range rows {
		titles = append(titles, r.Title)
	}
	return titles, nil
}

type advice struct {
	Title      string `json:"title"`
	Correction string `json:"correction"`
	Next       string `json:"next"`
	Advanced   string `json:"advanced"`
}

func main() {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.GeminiApiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatal(err)
	}

	allNotes, err := getAllNotes()
	if err != nil {
		log.Fatal(err)
	}

	modelName := "gemini-2.5-flash-preview-05-20"

	existing, err := getExistingTitles()
	if err != nil {
		log.Fatal(err)
	}
	existingTitles := strings.Join(existing, " / ")

	prompt := fmt.Sprintf(`あなたは情報科学の博士号を持っている経験豊富なエンジニアの先輩です。
私は情報科学を専攻している学生で、勉強したことをMarkdownでノートを取っています。
Markdownファイルのパスと内容を以下に貼り付けるので、あるトピックを1つ選んで、先輩の立場から私が情報科学をより深く学ぶきっかけになるためのアドバイスを作成してください。
title = タイトル。30字以内。**すでに存在するタイトルとは重複しない具体的な要約にすること**。
correction = ノートに明確な間違いや誤った理解の形跡に対する指摘（なければ空文字）
next = 大学教育で教えられる情報科学の範疇で、未勉強で次に学ぶべき内容の提案
advanced = 大学教育で教えられる情報科学の範疇を超えて、さらに高度で興味を持つ可能性が高い内容の提案
1つのアドバイスでは1つの項目について触れてください。集中して勉強できるように、できるだけ範囲を絞ってアドバイスを作成してください。
指摘の際には必ず"Path: "で示したファイル名を含めてください。

すでに存在するタイトル一覧: %s

%s`, existingTitles, allNotes)

	cfgGen := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   buildSchema(),
	}
	result, err := client.Models.GenerateContent(
		ctx,
		modelName,
		genai.Text(prompt),
		cfgGen,
	)
	if err != nil {
		log.Fatal(err)
	}

	var adv advice
	if err := json.Unmarshal([]byte(result.Text()), &adv); err != nil {
		log.Fatalf("JSON parse error: %v\noutput: %s", err, result.Text())
	}

	body := fmt.Sprintf(`## 誤りの指摘
%s

## 次に学ぶ内容
%s

## 発展的提案
%s
`, adv.Correction, adv.Next, adv.Advanced)

	cmd := exec.Command("gh", "issue", "create",
		"--title", adv.Title,
		"--body", body,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("failed to create issue: %v", err)
	}
}
