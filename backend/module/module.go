// Package module 是 mdm-product 唯一的装配入口。独立运行（cmd/server 的
// besdk.RunStandalone）与进外壳走同一个 New：模块只交回零件（HTTP handler、
// gRPC 注册函数、后台循环），谁去 Listen、谁开连接池、谁初始化 OTel 与信号
// 处理，全归调用方——这样同一份代码进外壳之后不会与别的成员互相覆盖。
//
// 这个组件是被所有人读的主数据 + 常规 CRUD，repo / service / http / grpc
// 四层直给，不用设计模式。
package module

import (
	"context"

	besdk "github.com/brickKit/be-sdk-go"
	productv1 "github.com/brickKit/mdm-product/gen/mdm/product/v1"
	"google.golang.org/grpc"

	grpcapi "github.com/brickKit/mdm-product/v2/backend/internal/grpc"
	httpapi "github.com/brickKit/mdm-product/v2/backend/internal/http"
	"github.com/brickKit/mdm-product/v2/backend/internal/partition"
	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	"github.com/brickKit/mdm-product/v2/backend/internal/service"
)

// New 构造 mdm-product 模块。签名是外壳与 RunStandalone 共同依赖的约定，
// 不改。
func New(ctx context.Context, rt *besdk.Runtime) (*besdk.Module, error) {
	// 配置只从 rt.Config 读，模块里不碰 os.Getenv：一个进程只有一份环境，
	// 进外壳后各成员的 PG_SCHEMA 会互相覆盖，不报错，模块就按别人的 schema
	// 读写数据。
	schema := rt.Config.StringOr("PG_SCHEMA", "mdm_product")
	role := schema + "_rw"

	// 连接池从 rt.DB 来，不自己 sql.Open：外壳里所有成员共用一个池，每次访问
	// 经 besdk.WithTx 在事务里 SET LOCAL ROLE / search_path 切到本组件。
	r := repo.New(rt.DB, role, schema)
	svc := service.New(r, rt.Logger)

	// HTTP：engine 必须用 besdk.NewGinEngine，它已挂好 OTel / request-id /
	// error→status / PII 脱敏日志 / RED 指标 / /healthz / /metrics。
	eng := besdk.NewGinEngine(rt)
	httpapi.RegisterRoutes(eng, svc)

	return &besdk.Module{
		HTTPHandler: eng,

		// gRPC 由调用方在 extraPorts["grpc"] 上 Listen；component.yaml 声明了
		// 这个端口，它就必须真的有服务在听。
		RegisterGRPC: func(gs *grpc.Server) {
			productv1.RegisterProductServiceServer(gs, grpcapi.New(svc))
		},

		// 后台循环：Outbox 推送 + 分区自动维护。两个循环必须并发跑，不能
		// 顺序调用——StartOutboxPump 是阻塞到 ctx 取消才返回的循环。
		Start: func(ctx context.Context) error {
			errCh := make(chan error, 2)
			go func() { errCh <- besdk.StartOutboxPump(ctx, rt.DB, schema, rt.NATS, rt.Logger) }()
			go func() { errCh <- partition.Start(ctx, rt.DB, role, schema, rt.Logger) }()

			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				return err // 返回 error，不 log.Fatal：进外壳后一个成员退出进程，同一外壳的成员全都下线
			}
		},
		Stop: func(ctx context.Context) error { return nil }, // 后台循环靠 ctx 退出
	}, nil
}
