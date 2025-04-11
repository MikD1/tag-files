using System.Reflection;
using Microsoft.OpenApi.Models;
using Swashbuckle.AspNetCore.SwaggerGen;

namespace TagFilesService.WebHost;

public class FileUploadOperationFilter : IOperationFilter
{
    public void Apply(OpenApiOperation operation, OperationFilterContext context)
    {
        IEnumerable<ParameterInfo> fileParams = context.MethodInfo.GetParameters()
            .Where(p => p.ParameterType == typeof(IFormFile));

        if (!fileParams.Any())
        {
            return;
        }

        operation.RequestBody = new()
        {
            Content =
            {
                ["multipart/form-data"] = new()
                {
                    Schema = new()
                    {
                        Type = "object",
                        Properties =
                        {
                            ["file"] = new()
                            {
                                Type = "string",
                                Format = "binary"
                            }
                        },
                        Required = new HashSet<string> { "file" }
                    }
                }
            }
        };
    }
}