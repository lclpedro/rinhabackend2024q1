package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
	_ "github.com/lib/pq"
)

var DB *sql.DB

func main() {
	initDB()

	app := fiber.New()
	app.Post("/clientes/:id/transacoes", InserirTransacao)
	app.Get("/clientes/:id/extrato", ExtratoConta)
	app.Listen(":9999")
}

func initDB() {
	host := os.Getenv("DB_HOSTNAME")
	db, err := sql.Open(
		"postgres",
		fmt.Sprintf(
			"host=%s port=5432 user=postgres password=postgres dbname=bancocentral sslmode=disable",
			host,
		),
	)
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1_000)
	db.SetMaxIdleConns(1_000)
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	DB = db
}

type Conta struct {
	Saldo  int64 `json:"total"`
	Limite int64 `json:"limite"`
}

type Extrato struct {
	Valor       int64  `json:"valor"`
	Tipo        string `json:"tipo"`
	Descricao   string `json:"descricao"`
	RealizadaEm string `json:"realizada_em"`
}

func (e Extrato) GetDescricao() string {
	if len(e.Descricao) > 10 {
		return e.Descricao[:10]
	}
	return e.Descricao
}

// POST transacao
const (
	queryGetExtrato = `
	SELECT
    c.saldo, c.limite, TO_CHAR(now(), 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') as data_hora,
    t.valor, t.tipo, t.descricao, TO_CHAR(t.realizada_em, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') as realizada_em
	FROM
			conta c INNER JOIN transacao t ON t.conta_id = c.id
	WHERE
			c.id=$1
	ORDER BY t.realizada_em desc
	LIMIT 10`
	queryInserirTransacao = "INSERT INTO transacao (conta_id, valor, tipo, descricao, realizada_em) VALUES ($1, $2, $3, $4, now())"
	queryAtualizarSaldo   = "UPDATE conta SET saldo = %s $1 WHERE id = $2 RETURNING saldo, limite"
)

func transacaoValida(extrato Extrato) bool {
	fmt.Println(extrato)
	if extrato.Tipo != "c" && extrato.Tipo != "d" {
		fmt.Println("Tipo inválido")
		return false
	}
	if extrato.Valor <= 0 {
		fmt.Println("Valor inválido")
		return false
	}
	if len(extrato.Descricao) == 0 || len(extrato.Descricao) > 10 {
		fmt.Println("Descrição inválida")
		return false
	}

	return true
}

func InserirTransacao(c *fiber.Ctx) error {
	ctx := context.Background()
	id := c.Params("id")
	bodyRequest := c.Body()
	transacao := Extrato{}
	sonic.Unmarshal(bodyRequest, &transacao)

	if !transacaoValida(transacao) {
		return c.Status(422).SendString(`{"message": "Transação inválida"}`)
	}

	conta := Conta{}
	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return c.Status(500).SendString(err.Error())
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(
		ctx, fmt.Sprintf(queryAtualizarSaldo, defineOperacao(transacao.Tipo)), transacao.Valor, id,
	).Scan(
		&conta.Saldo,
		&conta.Limite,
	)

	if errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("\nConta %s não existe\n", id)
		return c.Status(404).SendString(`{"message": "Conta não encontrada"}`)
	}
	if err != nil {
		fmt.Println(err.Error())
		return c.Status(500).SendString(`{"message": "Erro ao atualizar saldo da conta"}`)
	}

	if transacao.Tipo == "d" && conta.Saldo > -conta.Limite {
		return c.Status(422).SendString(`{"message": "Limite de conta excedido"}`)
	}

	if err := tx.Commit(); err != nil {
		fmt.Println(err.Error())
		return c.Status(500).SendString(err.Error())
	}

	// TODO: testar benchmark com e sem goroutine
	go func() {
		_, err = DB.ExecContext(
			ctx, queryInserirTransacao, id, transacao.Valor, transacao.Tipo, transacao.GetDescricao(),
		)
		if err != nil {
			fmt.Println(err.Error())
		}
	}()

	return c.JSON(conta)
}

type Response struct {
	Saldo             Saldo             `json:"saldo"`
	UltimasTransacoes []UltimaTransacao `json:"ultimas_transacoes"`
}

type Saldo struct {
	Total       int64  `json:"total"`
	DataExtrato string `json:"data_extrato"`
	Limite      int64  `json:"limite"`
}

type UltimaTransacao struct {
	Valor       int64  `json:"valor"`
	Tipo        string `json:"tipo"`
	Descricao   string `json:"descricao"`
	RealizadaEm string `json:"realizada_em"`
}

// GET extrato
func ExtratoConta(c *fiber.Ctx) error {
	ctx := context.Background()
	id := c.Params("id")
	response := Response{}

	rows, err := DB.QueryContext(ctx, queryGetExtrato, id)
	if err != nil {
		fmt.Println(err.Error())
		return c.Status(500).JSON(fiber.Map{
			"message": err.Error(),
		})
	}

	defer func() {
		if err := rows.Close(); err != nil {
			fmt.Println(err.Error())
		}
	}()

	var ultimasTransacoes = make([]UltimaTransacao, 10)
	index := 0
	for rows.Next() {
		err = rows.Scan(
			&response.Saldo.Total,
			&response.Saldo.Limite,
			&response.Saldo.DataExtrato,
			&ultimasTransacoes[index].Valor,
			&ultimasTransacoes[index].Tipo,
			&ultimasTransacoes[index].Descricao,
			&ultimasTransacoes[index].RealizadaEm,
		)

		if err != nil {
			fmt.Println(err.Error())
			return c.Status(500).JSON(fiber.Map{
				"message": err.Error(),
			})
		}
		index++
	}
	response.UltimasTransacoes = ultimasTransacoes

	return c.JSON(response)
}

// wrappers
func defineOperacao(tipo string) string {
	operacao := map[string]string{
		"c": "saldo +",
		"d": "saldo -",
	}
	return operacao[tipo]
}
