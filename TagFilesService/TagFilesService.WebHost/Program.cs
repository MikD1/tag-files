using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.Infrastructure;
using TagFilesService.Model;
using TagFilesService.WebHost;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen(x => { x.OperationFilter<FileUploadOperationFilter>(); });

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite("Data Source=tag-files.db"));
builder.Services.AddMinio(configure => configure
    .WithEndpoint("localhost:5010")
    .WithCredentials("admin", "12345678")
    .WithSSL(false)
    .Build());
builder.Services.AddScoped<FileStorage>();
builder.Services.AddScoped<ITagsRepository, TagsRepository>();
builder.Services.AddScoped<IMetadataService, MetadataService>();

WebApplication app = builder.Build();
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

app.Run();