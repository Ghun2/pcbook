package service

import (
	"bytes"
	"context"
	"errors"
	"github.com/Ghun2/pcbook/pb"
	"github.com/Ghun2/pcbook/util/in_error"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"log"
)

type LaptopServer struct {
	laptopStore LaptopStore
	imageStore  ImageStore
}

const maxImageSize = 1 << 20

func NewLaptopServer(laptopStore LaptopStore, imageStore ImageStore) *LaptopServer {
	return &LaptopServer{laptopStore, imageStore}
}

func (s *LaptopServer) CreateLaptop(ctx context.Context, req *pb.CreateLaptopRequest) (*pb.CreateLaptopResponse, error) {
	laptop := req.GetLaptop()
	log.Printf("receive a create-laptop request with id: %s\n", laptop.Id)

	if len(laptop.Id) > 0 {
		_, err := uuid.Parse(laptop.Id)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "laptop ID is not a valid UUID: %v", err)
		}
	} else {
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "cannot generate a new laptop ID: %v", err)
		}
		laptop.Id = id.String()
	}

	if err := in_error.ContextError(ctx); err != nil {
		return nil, err
	}

	err := s.laptopStore.Save(laptop)
	if err != nil {
		code := codes.Internal
		if errors.Is(err, in_error.ErrAlreadyExists) {
			code = codes.AlreadyExists
		}

		return nil, status.Errorf(code, "cannot save laptop to the store: %v", err)
	}

	log.Printf("saved laptop with id: %s\n", laptop.Id)

	res := &pb.CreateLaptopResponse{
		Id: laptop.Id,
	}
	return res, nil
}

func (s *LaptopServer) SearchLaptop(req *pb.SearchLaptopRequest, stream pb.LaptopService_SearchLaptopServer) error {
	filter := req.GetFilter()
	log.Printf("receive a search-laptop request with filter: %v", filter)

	err := s.laptopStore.Search(stream.Context(), filter, func(laptop *pb.Laptop) error {
		res := &pb.SearchLaptopResponse{
			Laptop: laptop,
		}
		err := stream.Send(res)
		if err != nil {
			return err
		}
		log.Printf("sent laptop with id: %s", laptop.GetId())
		return nil
	})
	if err != nil {
		return status.Errorf(codes.Internal, "unexpected error: %v", err)
	}
	return nil
}

func (s *LaptopServer) UploadImage(stream pb.LaptopService_UploadImageServer) error {
	req, err := stream.Recv()
	if err != nil {
		return in_error.LogError(status.Errorf(codes.Unknown, "cannot receive image info"))
	}

	laptopID := req.GetInfo().GetLaptopId()
	imageType := req.GetInfo().GetImageType()
	log.Printf("receive an upload-image request for laptop %s with image type %s", laptopID, imageType)

	laptop, err := s.laptopStore.Find(laptopID)
	if err != nil {
		return in_error.LogError(status.Errorf(codes.Internal, "cannot find laptop: %v", err))
	}
	if laptop == nil {
		return in_error.LogError(status.Errorf(codes.InvalidArgument, "laptop %s doesn't exist", laptopID))
	}

	imageData := bytes.Buffer{}
	imageSize := 0

	for {
		if err := in_error.ContextError(stream.Context()); err != nil {
			return err
		}
		log.Println("waiting to receive more data")

		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("no more data")
			break
		}
		if err != nil {
			return in_error.LogError(status.Errorf(codes.Unknown, "cannot receive chunk data: %v", err))
		}

		chunk := req.GetChunkData()
		size := len(chunk)

		log.Printf("received a chunk with size: %d", size)

		imageSize += size
		if imageSize > maxImageSize {
			return in_error.LogError(status.Errorf(codes.InvalidArgument, "image is too large: %d > %d", imageSize, maxImageSize))
		}

		_, err = imageData.Write(chunk)
		if err != nil {
			return in_error.LogError(status.Errorf(codes.Internal, "cannot write chuck data: %v", err))
		}
	}

	imageID, err := s.imageStore.Save(laptopID, imageType, imageData)
	if err != nil {
		return in_error.LogError(status.Errorf(codes.Internal, "cannot save image to the store: %v", err))
	}

	res := &pb.UploadImageResponse{
		Id:   imageID,
		Size: uint32(imageSize),
	}

	err = stream.SendAndClose(res)
	if err != nil {
		return in_error.LogError(status.Errorf(codes.Unknown, "cannot send response: %v", err))
	}

	log.Printf("saved image with id: %s, size: %d\n", imageID, imageSize)

	return nil
}
