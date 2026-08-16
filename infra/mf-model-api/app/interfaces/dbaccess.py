from fastapi import APIRouter
from pydantic import StrictFloat, StrictStr, Field, StrictInt, conint,  conlist, constr,BaseModel

model_quota_router = APIRouter()


# External small-model persistence record.
class AddExternalSmallModelInfo(BaseModel):
    model_id: StrictStr = Field(description="Configuration ID", default="")
    model_name: StrictStr = Field(description="Model name", default="")
    model_type: StrictStr = Field(description="Model type", default="")
    model_config: dict
    adapter: bool = Field(default=False, description="Whether to enable the adapter service")
    adapter_code: StrictStr = Field(default=None, description="Adapter code")
# Large-model quota record.
class ModelQuotaInfo (BaseModel):
    conf_id: StrictStr = Field(description="Configuration ID")
    model_id:StrictStr = Field(description="Bound model ID")
    billing_type: StrictInt = Field(description="Billing type: 0 combined, 1 separate input/output")
    input_tokens:StrictFloat = Field(description="Total input-token quota")
    output_tokens:StrictFloat= Field(description="Total output-token quota")
    currency_type:StrictInt = Field(description="Billing currency: 0 CNY, 1 USD")
    referprice_in:StrictFloat = Field(description="Input-token unit price")
    referprice_out:StrictFloat = Field(description="Output-token unit price")
    create_time:StrictStr= Field(description="Creation time")
    update_time:StrictStr= Field(description="Update time")
    num_type:conlist(conint(ge=0, le=5), min_items=2, max_items=2)
    price_type: conlist(constr(regex=r'^(thousand|million)$'), min_items=2, max_items=2)

# Per-user large-model quota record.
class ModelUserQuotaInfo (BaseModel):
    conf_id: StrictStr = Field(description="Configuration ID",default="")
    model_id:StrictStr = Field(description="Bound model ID",default="")
    user_id:StrictStr = Field(description="User ID",default="")
    input_tokens:StrictFloat = Field(description="Total input-token quota",default=0)
    output_tokens:StrictFloat= Field(description="Total output-token quota",default=0)
    create_time:StrictStr= Field(description="Creation time",default="")
    update_time:StrictStr= Field(description="Update time",default="")
    num_type:conlist(conint(ge=0, le=3), min_items=2, max_items=2)

# Large-model usage audit record.
class ModelUsedAuditInfo (BaseModel):
    conf_id: StrictStr = Field(description="Configuration ID",default="")
    model_id:StrictStr = Field(description="Model ID",default="")
    user_id:StrictStr = Field(description="User ID",default="")
    input_tokens:StrictInt = Field(description="Input tokens used",default=0)
    output_tokens:StrictInt= Field(description="Output tokens used",default=0)
    total_price:float = Field(description="Total charge",default=0.0)
    create_time:StrictStr= Field(description="Creation time",default="")
    currency_type:StrictInt = Field(description="Billing currency: 0 CNY, 1 USD")
    price_type: conlist(constr(regex=r'^(thousand|million)$'), min_items=2, max_items=2)
    referprice_in: StrictFloat = Field(description="Input-token unit price", default=0.0)
    referprice_out: StrictFloat = Field(description="Output-token unit price", default=0.0)
