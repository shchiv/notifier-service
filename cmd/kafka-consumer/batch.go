package main

// -- pgx CopyFromSource support
type batchPayloadStruct struct {
	batch        [][]interface{}
	positionNext int
	positionAdd  int
}

func newPayloadBatch(size int) *batchPayloadStruct {
	sliceOfSlices := make([][]interface{}, size)
	for i := 0; i < size; i++ {
		sliceOfSlices[i] = make([]interface{}, 1)
	}

	return &batchPayloadStruct{
		batch:        sliceOfSlices,
		positionNext: -1,
	}
}
func (pb *batchPayloadStruct) Reset() {
	pb.positionAdd = 0
	pb.positionNext = -1
}

func (pb *batchPayloadStruct) Add(value any) {
	pb.batch[pb.positionAdd][0] = value
	pb.positionAdd++
}

func (pb *batchPayloadStruct) Size() int {
	return pb.positionAdd
}

func (pb *batchPayloadStruct) Next() bool {
	pb.positionNext++
	return pb.positionNext < pb.Size()
}

func (pb *batchPayloadStruct) Values() ([]any, error) {
	return pb.batch[pb.positionNext], nil
}

func (pb *batchPayloadStruct) Err() error {
	return nil
}
