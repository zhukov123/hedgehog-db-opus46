#!/bin/bash
set -e

BASE_URL="${HEDGEHOG_URL:-http://localhost:8080}"
# Create tables on all cluster nodes so node 2 and 3 have tables too (8081 8082 8083)
CLUSTER_PORTS="${CLUSTER_PORTS:-8081 8082 8083}"
CTL="./bin/hedgehogctl"

if [ ! -f "$CTL" ]; then
    echo "Building..."
    make build
fi

echo "Seeding data to $BASE_URL..."

# Create tables on every cluster node so node 2 and 3 UIs show tables
echo "Creating tables on all nodes..."
for port in $CLUSTER_PORTS; do
  url="http://127.0.0.1:$port"
  HEDGEHOG_URL="$url" $CTL create-table users 2>/dev/null || true
  HEDGEHOG_URL="$url" $CTL create-table products 2>/dev/null || true
  HEDGEHOG_URL="$url" $CTL create-table orders 2>/dev/null || true
done
# Ensure primary has tables (in case CLUSTER_PORTS didn't include BASE_URL's port)
$CTL create-table users 2>/dev/null || true
$CTL create-table products 2>/dev/null || true
$CTL create-table orders 2>/dev/null || true

# Seed users
echo "Seeding users..."
for i in $(seq 1 20); do
    $CTL put users "user-$(printf '%03d' $i)" "{\"name\": \"User $i\", \"email\": \"user$i@example.com\", \"age\": $((20 + RANDOM % 40))}" > /dev/null
done

# Seed products
echo "Seeding products..."
PRODUCTS=("Laptop" "Phone" "Tablet" "Headphones" "Monitor" "Keyboard" "Mouse" "Webcam" "Speaker" "Charger")
for i in "${!PRODUCTS[@]}"; do
    idx=$(printf '%03d' $((i + 1)))
    price=$(( (RANDOM % 900 + 100) ))
    $CTL put products "prod-$idx" "{\"name\": \"${PRODUCTS[$i]}\", \"price\": $price.99, \"in_stock\": true}" > /dev/null
done

# Seed orders
echo "Seeding orders..."
for i in $(seq 1 15); do
    idx=$(printf '%03d' $i)
    user_idx=$(printf '%03d' $((RANDOM % 20 + 1)))
    prod_idx=$(printf '%03d' $((RANDOM % 10 + 1)))
    qty=$((RANDOM % 5 + 1))
    $CTL put orders "order-$idx" "{\"user_id\": \"user-$user_idx\", \"product_id\": \"prod-$prod_idx\", \"quantity\": $qty, \"status\": \"completed\"}" > /dev/null
done

echo ""
echo "Seed data created:"
echo "  - 20 users"
echo "  - 10 products"
echo "  - 15 orders"
echo ""
echo "Try: $CTL scan users"
