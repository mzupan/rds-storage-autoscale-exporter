package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	maxAllocatedStorage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rds_max_allocated_storage_gigabytes",
			Help: "Maximum storage (in gigabytes) that RDS instance can auto-scale to.",
		},
		[]string{"instance"},
	)

	currentUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rds_current_usage_gigabytes",
			Help: "Current storage usage of the RDS instance in gigabytes.",
		},
		[]string{"instance"},
	)
)

func init() {
	prometheus.MustRegister(maxAllocatedStorage)
	prometheus.MustRegister(currentUsage)
}

func loadAWSConfig() aws.Config {
	region := "us-east-1"
	if os.Getenv("AWS_REGION") != "" {
		region = os.Getenv("AWS_REGION")
	}

	cfgOptions := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		// get the session if one is set in the environment
		session := ""
		if os.Getenv("AWS_SESSION_TOKEN") != "" {
			session = os.Getenv("AWS_SESSION_TOKEN")
		}

		cfgOptions = append(cfgOptions, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
			session,
		)))
	}

	// Load the AWS config with the provided options
	cfg, err := config.LoadDefaultConfig(context.TODO(), cfgOptions...)
	if err != nil {
		log.Fatalf("unable to load SDK config: %v", err)
	}

	return cfg
}

func main() {
	log.Println("Starting application...")

	// Load the AWS Configuration
	cfg := loadAWSConfig()

	// Create an Amazon RDS and CloudWatch service client
	rdsSvc := rds.NewFromConfig(cfg)
	cwSvc := cloudwatch.NewFromConfig(cfg)

	// Start Prometheus HTTP server
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":9761", nil))
	}()

	// Continuously update metrics
	for {
		updateMetrics(rdsSvc, cwSvc)
		time.Sleep(5 * time.Minute) // Adjust the frequency of updates as needed
	}
}

func updateMetrics(rdsSvc *rds.Client, cwSvc *cloudwatch.Client) {
	log.Println("Updating metrics...")

	// Send the request, and get the response for RDS instances
	resp, err := rdsSvc.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{})
	if err != nil {
		log.Printf("unable to describe DB instances: %v", err)
		return
	}

	// Process each DB instance
	for _, dbInstance := range resp.DBInstances {
		instanceID := aws.ToString(dbInstance.DBInstanceIdentifier)

		// Get FreeStorageSpace from CloudWatch
		metricData, err := cwSvc.GetMetricData(context.TODO(), &cloudwatch.GetMetricDataInput{
			StartTime: aws.Time(time.Now().Add(-3 * time.Hour)),
			EndTime:   aws.Time(time.Now()),
			MetricDataQueries: []types.MetricDataQuery{
				{
					Id: aws.String("m1"),
					MetricStat: &types.MetricStat{
						Metric: &types.Metric{
							Namespace:  aws.String("AWS/RDS"),
							MetricName: aws.String("FreeStorageSpace"),
							Dimensions: []types.Dimension{
								{
									Name:  aws.String("DBInstanceIdentifier"),
									Value: dbInstance.DBInstanceIdentifier,
								},
							},
						},
						Period: aws.Int32(3600),
						Stat:   aws.String("Average"),
					},
				},
			},
		})
		if err != nil {
			log.Printf("unable to get metric data for instance %s: %v", instanceID, err)
			continue
		}

		// Assume there's data and calculate usage
		if len(metricData.MetricDataResults) > 0 && len(metricData.MetricDataResults[0].Values) > 0 {
			totalSpace := aws.ToInt32(dbInstance.AllocatedStorage) //* 1073741824 // GB to Bytes

			currentUsage.WithLabelValues(instanceID).Set(float64(totalSpace))

			if dbInstance.MaxAllocatedStorage != nil {
				maxAllocatedBytes := aws.ToInt32(dbInstance.MaxAllocatedStorage) //* 1073741824 // GB to Bytes
				maxAllocatedStorage.WithLabelValues(instanceID).Set(float64(maxAllocatedBytes))
			}
		} else {
			log.Printf("no metric data found for instance %s", instanceID)
		}
	}

	log.Println("Metrics updated successfully.")
}
