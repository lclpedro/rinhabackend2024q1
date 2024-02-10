CREATE TABLE transacao (
  id serial PRIMARY KEY,
  valor integer,
  tipo char(1),
  descricao varchar(10),
  realizada_em timestamp,
  conta_id integer
);
CREATE INDEX idx_transacao_id ON transacao (id, tipo);

CREATE TABLE conta (
  id serial PRIMARY KEY,
  saldo integer,
  limite integer
);
CREATE INDEX idx_conta_id ON conta (id);

insert into conta (id, limite, saldo) values 
(1, 100000, 0), 
(2, 80000, 0), 
(3, 1000000, 0), 
(4, 10000000, 0), 
(5, 500000, 0);
