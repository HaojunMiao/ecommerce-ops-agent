-- 模型部署价格只用于调用成本统计。
ALTER TABLE model_deployments
    ADD COLUMN input_price_per_million NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN output_price_per_million NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN cached_input_price_per_million NUMERIC NOT NULL DEFAULT 0,
    ADD CONSTRAINT model_deployment_prices_nonnegative CHECK (
        input_price_per_million >= 0 AND output_price_per_million >= 0 AND
        cached_input_price_per_million >= 0
    );
