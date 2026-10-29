package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "libratrack/gen/inventory"
)

func cmdAvailability(ctx context.Context, client pb.InventoryServiceClient, bookID string) error {
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	response, err := client.GetBookAvailability(ctx, &pb.GetBookAvailabilityRequest{BookId: bookID})
	if err != nil {
		return err
	}
	printMsg(response)
	return nil
}

func cmdWatchAvailability(ctx context.Context, client pb.InventoryServiceClient, bookID string) error {
	if *maxUpdates < 0 || int64(*maxUpdates) > math.MaxInt32 {
		return fmt.Errorf("max must be between 0 and %d", math.MaxInt32)
	}
	if *streamTimeout <= 0 {
		return fmt.Errorf("stream-timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, *streamTimeout)
	defer cancel()
	stream, err := client.WatchBookAvailability(ctx, &pb.WatchBookAvailabilityRequest{BookId: bookID, MaxUpdates: int32(*maxUpdates)})
	if err != nil {
		return err
	}
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println("потік завершено сервером (io.EOF) — все гаразд")
			return nil
		}
		if err != nil {
			if status.Code(err) == codes.Canceled && errors.Is(ctx.Err(), context.Canceled) {
				fmt.Println("потік скасовано клієнтом (Ctrl+C)")
				return nil
			}
			return err
		}
		value := response.GetAvailability()
		fmt.Printf("book=%s total=%d available=%d borrowed=%d lost=%d revision=%d\n", value.GetBookId(), value.GetTotal(), value.GetAvailable(), value.GetBorrowed(), value.GetLost(), value.GetRevision())
	}
}
