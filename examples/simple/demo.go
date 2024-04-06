package main

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shengyanli1982/conecta"
	"github.com/shengyanli1982/workqueue"
)

// Demo 是一个包含 value 字段的结构体
// Demo is a struct that contains a value field
type Demo struct {
	// value 是一个字符串
	// value is a string
	value string

	// id 是一个字符串
	// id is a string
	id string
}

// GetValue 是一个方法，它返回 Demo 结构体中的 value 字段
// GetValue is a method that returns the value field in the Demo struct
func (d *Demo) GetValue() string {
	// 返回 value 字段
	// Return the value field
	return d.value
}

// GetID 是一个方法，它返回 Demo 结构体中的 id 字段
// GetID is a method that returns the id field in the Demo struct
func (d *Demo) GetID() string {
	// 返回 id 字段
	// Return the id field
	return d.id
}

// NewFunc 是一个函数，它创建并返回一个新的 Demo 结构体
// NewFunc is a function that creates and returns a new Demo struct
func NewFunc() (any, error) {
	// 创建一个新的 Demo 结构体，其 value 字段被设置为 "test"
	// Create a new Demo struct with its value field set to "test"
	return &Demo{value: "test", id: uuid.NewString()}, nil
}

func main() {
	// 创建一个工作队列
	// Create a work queue.
	baseQ := workqueue.NewSimpleQueue(nil)

	// 创建一个 Conecta 池
	// Create a Conecta pool.
	conf := conecta.NewConfig().
		// 使用 NewFunc 函数作为新建连接的函数
		// Use the NewFunc function as the function to create a new connection.
		WithNewFunc(NewFunc)

	// 使用 conecta.New 函数创建一个新的连接池
	// Use the conecta.New function to create a new connection pool.
	pool, err := conecta.New(baseQ, conf)

	// 检查是否在创建连接池时出现错误
	// Check if there was an error while creating the connection pool.
	if err != nil {
		// 如果创建连接池时出错，打印错误并返回
		// If an error occurs while creating the connection pool, print the error and return.
		fmt.Println("!! [main] create pool error:", err)
		return
	}
	// 使用 defer 关键字确保在函数结束时停止池
	// Use the defer keyword to ensure that the pool is stopped when the function ends
	defer pool.Stop()

	// 使用 for 循环从池中获取数据
	// Use a for loop to get data from the pool
	for i := 0; i < 10; i++ {
		// 使用 GetOrCreate 方法从池中获取数据
		// Use the GetOrCreate method to get data from the pool
		if data, err := pool.GetOrCreate(); err != nil {
			// 如果从池中获取数据时出错，打印错误并返回
			// If an error occurs while getting data from the pool, print the error and return
			fmt.Println("!! [main] get data error:", err)
			return

		} else {
			// 打印从池中获取的数据
			// Print the data obtained from the pool
			fmt.Printf(">> [main] get data: %s, id: %s\n", fmt.Sprintf("%s_%v", data.(*Demo).GetValue(), i), data.(*Demo).GetID())

			// 使用 Put 方法将数据放回池中
			// Use the Put method to put the data back into the pool
			if err := pool.Put(data); err != nil {
				// 如果将数据放回池中时出错，打印错误并返回
				// If an error occurs while putting the data back into the pool, print the error and return
				fmt.Println("!! [main] put data error:", err)
				return
			}
		}
	}
}
