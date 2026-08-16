// Package conecta 是一个类型无关的通用对象池管理器，可包装任意对象（如连接、客户端句柄）并管理其生命周期：后台健康检查、按需创建与清理。主模块零外部依赖，队列实现可插拔。
// Package conecta is a type-agnostic generic object pool manager that wraps arbitrary objects (such as connections and client handles) and manages their lifecycle: background health checking, on-demand creation and cleanup. The main module has zero external dependencies and supports pluggable queue implementations.
package conecta
