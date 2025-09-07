using Microsoft.OpenApi.Models;
using Swashbuckle.AspNetCore.SwaggerGen;

namespace TagFilesService.WebHost;

public class RequireNonNullablePropertiesSchemaFilter : ISchemaFilter
{
    public void Apply(OpenApiSchema schema, SchemaFilterContext context)
    {
        if (schema.Properties == null)
        {
            return;
        }

        foreach (KeyValuePair<string, OpenApiSchema> prop in schema.Properties)
        {
            schema.Required.Add(prop.Key);
        }
    }
}