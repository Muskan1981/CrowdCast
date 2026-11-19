package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var firstNames = []string{
	"Aarav", "Vivaan", "Aditya", "Vihaan", "Arjun",
	"Sai", "Reyansh", "Aanya", "Diya", "Ananya",
	"Ishita", "Priya", "Sneha", "Rohan", "Kabir",
	"Meera", "Tara", "Neha", "Rahul", "Pooja",
	"Vikram", "Kavya", "Tanvi", "Nikhil", "Siddharth",
}

var lastNames = []string{
	"Sharma", "Verma", "Mehta", "Rao", "Patel",
	"Gupta", "Reddy", "Nair", "Joshi", "Iyer",
	"Singh", "Bhat", "Kumar", "Chatterjee", "Das",
	"Kapoor", "Chopra", "Deshmukh", "Menon", "Malhotra",
}

var colors = []string{
	"mint", "peach", "lilac", "amber", "rose", "sky",
}

// GenerateRandomDisplayName creates a realistic Indian name (First + Last).
func GenerateRandomDisplayName() string {
	fIdx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(firstNames))))
	lIdx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(lastNames))))
	return fmt.Sprintf("%s %s", firstNames[fIdx.Int64()], lastNames[lIdx.Int64()])
}

// GenerateRandomColor selects a random UI color identifier.
func GenerateRandomColor() string {
	idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(colors))))
	return colors[idx.Int64()]
}

// City weights per specification:
// Mumbai 28, Delhi NCR 24, Bengaluru 19, Hyderabad 14, Other 15 (Total = 100)
type weightedCity struct {
	City   string
	Weight int
}

var cityDistribution = []weightedCity{
	{"Mumbai", 28},
	{"Delhi NCR", 24},
	{"Bengaluru", 19},
	{"Hyderabad", 14},
	{"Other", 15},
}

// PickWeightedCity selects a city based on the weighted demographic distribution.
func PickWeightedCity() string {
	totalWeight := 100
	n, err := rand.Int(rand.Reader, big.NewInt(int64(totalWeight)))
	if err != nil {
		return "Mumbai"
	}

	val := int(n.Int64())
	cum := 0
	for _, item := range cityDistribution {
		cum += item.Weight
		if val < cum {
			return item.City
		}
	}
	return "Other"
}
