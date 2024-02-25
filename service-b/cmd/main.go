package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

var tracer = otel.Tracer("microservice-tracer")

func initProvider(serviceName, collectorUrl string) (func(context.Context) error, error) {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	conn, err := grpc.Dial(collectorUrl,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection to collector: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

func init() {
	// remover isso depois pois vai usar o dockerfile
	// viper.SetConfigFile(".env")
	// viper.ReadInConfig()

	viper.AutomaticEnv()
}

func main() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-sigChan
		log.Println("Received shutdown signal. Shutting down gracefully...")
		cancel()
	}()

	shutdown, err := initProvider(viper.GetString("OTEL_SERVICE_NAME"), viper.GetString("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		log.Fatalf("failed to initialize provider: %v", err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Fatalf("failed to shutdown TraceProvider: %v", err)
		}
	}()

	r := mux.NewRouter()
	r.HandleFunc("/city-weather", cityWeatherHandler)

	srv := &http.Server{
		Addr:         ":" + viper.GetString("HTTP_PORT"),
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("Server started at http://localhost:%s\n", viper.GetString("HTTP_PORT"))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v\n", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v\n", err)
	}

	log.Println("Server shutdown completed.")
}

func getViaCep(ctx context.Context, zipCode string, w http.ResponseWriter, r *http.Request) *ViaCep {
	carrier := propagation.HeaderCarrier(r.Header)
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)

	ctx, span := tracer.Start(ctx, "getViaCep")
	defer span.End()

	url := fmt.Sprintf("http://viacep.com.br/ws/%s/json/", zipCode)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		span.RecordError(fmt.Errorf("failed to create request (viacep): %w", err))
		http.Error(w, fmt.Sprintf("Failed to create request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		span.RecordError(fmt.Errorf("failed to make HTTP request (viacep): %w", err))
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		span.RecordError(fmt.Errorf("unexpected status code (viacep): %d", res.StatusCode))
		log.Printf("Unexpected status code (viacep): %d", res.StatusCode)

		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	var bodyBytes []byte
	if bodyBytes, err = io.ReadAll(res.Body); err != nil {
		span.RecordError(fmt.Errorf("failed to read response body: %w", err))
		http.Error(w, "Failed to read response body: "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	var viaCepErrorResponse ViaCepError
	if err := json.Unmarshal(bodyBytes, &viaCepErrorResponse); err != nil {
		span.RecordError(fmt.Errorf("failed to decode response (viacep): %w", err))
		http.Error(w, "Failed to decode response (viacep): "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	if viaCepErrorResponse.Erro {
		span.RecordError(fmt.Errorf("cannot find zipcode"))
		http.Error(w, "Cannot find zipcode", http.StatusNotFound)
		return nil
	}

	var viaCepResponse ViaCep
	if err := json.Unmarshal(bodyBytes, &viaCepResponse); err != nil {
		span.RecordError(fmt.Errorf("failed to decode response (viacep): %w", err))
		http.Error(w, "Failed to decode response (viacep): "+err.Error(), http.StatusInternalServerError)
		return nil
	}

	if viaCepResponse.Localidade == "" {
		span.RecordError(fmt.Errorf("invalid zipcode"))
		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	return &viaCepResponse
}

func getWeather(ctx context.Context, cityName string, w http.ResponseWriter, r *http.Request) *Weather {
	carrier := propagation.HeaderCarrier(r.Header)
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)

	ctx, span := tracer.Start(ctx, "getWeather")
	defer span.End()

	var response Weather

	cityNameEncoded := neturl.QueryEscape(cityName)
	url := fmt.Sprintf("http://api.weatherapi.com/v1/current.json?key=a91eb948a337442782b123810242601&q=%s", cityNameEncoded)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		span.RecordError(fmt.Errorf("failed to create request (weather): %w", err))
		http.Error(w, fmt.Sprintf("Failed to create request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		span.RecordError(fmt.Errorf("failed to make HTTP request (weather): %w", err))
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		span.RecordError(fmt.Errorf("unexpected status code (weather): %d", res.StatusCode))
		log.Printf("Unexpected status code (weather): %d", res.StatusCode)

		http.Error(w, "Invalid zipcode", http.StatusUnprocessableEntity)
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		span.RecordError(fmt.Errorf("failed to decode response (weather): %w", err))
		http.Error(w, fmt.Sprintf("Failed to decode response (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	return &response
}

func cityWeatherHandler(w http.ResponseWriter, r *http.Request) {
	carrier := propagation.HeaderCarrier(r.Header)
	ctx := r.Context()
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)

	ctx, span := tracer.Start(ctx, "cityWeatherHandler")
	defer span.End()

	if !validParams(w, r) {
		span.RecordError(fmt.Errorf("invalid parameters"))
		return
	}

	zipCode := r.URL.Query().Get("zipcode")

	viacepReturn := getViaCep(ctx, zipCode, w, r)
	if viacepReturn == nil {
		span.RecordError(fmt.Errorf("failed to get viacep"))
		return
	}

	cityName := viacepReturn.Localidade

	weatherReturn := getWeather(ctx, cityName, w, r)
	if weatherReturn == nil {
		span.RecordError(fmt.Errorf("failed to get weather"))
		return
	}

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
