package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/spf13/viper"
)

type ViaCepError struct {
	Erro bool `json:"erro"`
}

type ViaCep struct {
	Cep         string `json:"cep"`
	Logradouro  string `json:"logradouro"`
	Complemento string `json:"complemento"`
	Bairro      string `json:"bairro"`
	Localidade  string `json:"localidade"`
	Uf          string `json:"uf"`
	Ibge        string `json:"ibge"`
	Gia         string `json:"gia"`
	Ddd         string `json:"ddd"`
	Siafi       string `json:"siafi"`
}

type Weather struct {
	Location struct {
		Name           string  `json:"name"`
		Region         string  `json:"region"`
		Country        string  `json:"country"`
		Lat            float64 `json:"lat"`
		Lon            float64 `json:"lon"`
		TzID           string  `json:"tz_id"`
		LocaltimeEpoch int     `json:"localtime_epoch"`
		Localtime      string  `json:"localtime"`
	} `json:"location"`
	Current struct {
		TempC     float64 `json:"temp_c"`
		Condition struct {
		} `json:"condition"`
	} `json:"current"`
}

type TemperatureWithCity struct {
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

	mux.HandleFunc("/city-weather", cityWeatherHandler)

	log.Fatal(srv.ListenAndServe())
}

func getViaCep(zipCode string, w http.ResponseWriter, r *http.Request) *ViaCep {
	url := fmt.Sprintf("http://viacep.com.br/ws/%s/json/", zipCode)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code (viacep): %d", res.StatusCode)

		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	var bodyBytes []byte
	if bodyBytes, err = io.ReadAll(res.Body); err != nil {
		http.Error(w, "Failed to read response body: "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	var viaCepErrorResponse ViaCepError
	if err := json.Unmarshal(bodyBytes, &viaCepErrorResponse); err != nil {
		http.Error(w, "Failed to decode response (viacep): "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	if viaCepErrorResponse.Erro {
		http.Error(w, "Cannot find zipcode", http.StatusNotFound)
		return nil
	}

	var viaCepResponse ViaCep
	if err := json.Unmarshal(bodyBytes, &viaCepResponse); err != nil {
		http.Error(w, "Failed to decode response (viacep): "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	if viaCepResponse.Localidade == "" {
		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	return &viaCepResponse
}

func getWeather(cityName string, w http.ResponseWriter, r *http.Request) *Weather {
	var response Weather

	cityNameEncoded := neturl.QueryEscape(cityName)
	url := fmt.Sprintf("http://api.weatherapi.com/v1/current.json?key=a91eb948a337442782b123810242601&q=%s", cityNameEncoded)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code (weather): %d", res.StatusCode)

		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode response (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	return &response
}

func cityWeatherHandler(w http.ResponseWriter, r *http.Request) {
	if !validParams(w, r) {
		return
	}

	zipCode := r.URL.Query().Get("zipcode")

	viacepReturn := getViaCep(zipCode, w, r)
	if viacepReturn == nil {
		return
	}

	cityName := viacepReturn.Localidade

	weatherReturn := getWeather(cityName, w, r)

	temperatureWithCity := TemperatureWithCity{
		Celsius:    weatherReturn.Current.TempC,
		Fahrenheit: (weatherReturn.Current.TempC * 9 / 5) + 32,
		Kelvin:     weatherReturn.Current.TempC + 273.15,
		CityName:   cityName,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(temperatureWithCity)
}

func validParams(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("zipcode") == "" {
		http.Error(w, "Missing 'zipcode' parameter", http.StatusBadRequest)
		return false
	}

	return true
}
