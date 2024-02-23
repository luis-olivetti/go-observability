package main

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/spf13/viper"
)

type Message struct {
	ZipCode string `json:"cep"`
}

type Temperature struct {
	Celsius    float64 `json:"temp_C"`
	Fahrenheit float64 `json:"temp_F"`
	Kelvin     float64 `json:"temp_K"`
	CityName   string  `json:"city"`
}

func init() {
	// remover isso depois pois vai usar o dockerfile
	viper.SetConfigFile(".env")
	viper.ReadInConfig()

	viper.AutomaticEnv()
}

func main() {
	mux := http.NewServeMux()

	srv := &http.Server{
		Addr:         ":" + viper.GetString("PORT"),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	mux.HandleFunc("/city-by-zipcode", zipcodeHandler)

	log.Fatal(srv.ListenAndServe())
}

func zipcodeHandler(w http.ResponseWriter, r *http.Request) {
	var msg Message
	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	zipCodeRegex := regexp.MustCompile(`^\d{8}$`)
	if !zipCodeRegex.MatchString(msg.ZipCode) {
		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	resp, err := http.Get("http://localhost:8181/city-weather?zipcode=" + msg.ZipCode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Verificar o código de status da resposta do serviço B
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to fetch city weather", http.StatusInternalServerError)
		return
	}

	var cityWeatherResponse Temperature
	err = json.NewDecoder(resp.Body).Decode(&cityWeatherResponse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Responder com uma mensagem de sucesso
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cityWeatherResponse)
}
