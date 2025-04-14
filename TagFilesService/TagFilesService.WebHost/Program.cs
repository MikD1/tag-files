using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.Infrastructure;
using TagFilesService.Library;
using TagFilesService.Model;
using TagFilesService.WebHost;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen();
builder.Services.AddMediatR(cfg => cfg.RegisterServicesFromAssembly(typeof(FilesProcessing).Assembly));
builder.Services.AddCors(x => x.AddPolicy("AllowAll", policy =>
{
    policy
        .AllowAnyOrigin()
        .AllowAnyMethod()
        .AllowAnyHeader();
}));

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite("Data Source=./../../data/tag-files.db"));
builder.Services.AddMinio(configure => configure
    .WithEndpoint("localhost:5010")
    .WithCredentials("admin", "12345678")
    .WithSSL(false)
    .Build());
builder.Services.AddScoped<MetadataService>();
builder.Services.AddScoped<FilesProcessing>();
builder.Services.AddScoped<ITagsRepository, TagsRepository>();
builder.Services.AddHostedService<TemporaryBucketWatcher>();

WebApplication app = builder.Build();
app.UseCors("AllowAll");
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

app.Run();