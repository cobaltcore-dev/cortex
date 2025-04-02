package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"

	"github.com/rabbitmq/amqp091-go"
)

func main() {
	url := os.Getenv("AMQP_URL")
	conn, err := amqp091.Dial(url)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	// Open a channel
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Failed to open a channel: %v", err)
	}
	defer ch.Close()

	// Declare a temporary exchange to bind to all topics
	exchangeName := "nova"
	err = ch.ExchangeDeclare(
		exchangeName, // name
		"topic",      // type
		false,        // durable
		false,        // auto-deleted
		false,        // internal
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		log.Fatalf("Failed to declare an exchange: %v", err)
	}

	// Declare a temporary queue
	//nolint:gosec // We don't care if the queue id is cryptographically secure.
	queueID := fmt.Sprintf("cortex-amqp-queue-%d", rand.Intn(1_000_000))
	q, err := ch.QueueDeclare(
		queueID, // name (empty means a random name)
		false,   // durable
		true,    // delete when unused
		true,    // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		log.Fatalf("Failed to declare a queue: %v", err)
	}

	// Bind the queue to the exchange with a wildcard routing key
	err = ch.QueueBind(
		q.Name,       // queue name
		"#",          // routing key (wildcard for all topics)
		exchangeName, // exchange
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		log.Fatalf("Failed to bind the queue: %v", err)
	}

	// Start consuming messages
	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		true,   // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		log.Fatalf("Failed to register a consumer: %v", err)
	}

	log.Println("Waiting for messages. To exit press CTRL+C")

	// Print messages as they arrive
	for msg := range msgs {
		log.Printf("Received message: %s", msg.Body)
	}
}
