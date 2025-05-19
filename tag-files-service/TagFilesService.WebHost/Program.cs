using System.Text.Json.Serialization;
using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.FilesProcessing;
using TagFilesService.Infrastructure;
using TagFilesService.Library;
using TagFilesService.Model;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services
    .AddControllers()
    .AddJsonOptions(options => { options.JsonSerializerOptions.Converters.Add(new JsonStringEnumConverter()); });
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen();
builder.Services.AddMediatR(cfg => cfg
    .RegisterServicesFromAssemblyContaining<FilesProcessing>()
    .RegisterServicesFromAssemblyContaining<VideoConversionService>());
builder.Services.AddCors(x => x.AddPolicy("AllowAll", policy =>
{
    policy
        .AllowAnyOrigin()
        .AllowAnyMethod()
        .AllowAnyHeader();
}));

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite(builder.Configuration.GetConnectionString("SQLite")));
builder.Services.AddMinio(configure => configure
    .WithEndpoint(builder.Configuration["S3:Url"])
    .WithCredentials(builder.Configuration["S3:Username"], builder.Configuration["S3:Password"])
    .WithSSL(false)
    .Build());
builder.Services.AddScoped<MetadataService>();
builder.Services.AddScoped<FilesProcessing>();
builder.Services.AddScoped<ITagsRepository, TagsRepository>();

// builder.Services.AddHostedService<TemporaryBucketWatcher>();
builder.Services.AddSingleton<VideoConversionQueue>();
builder.Services.AddHostedService<VideoConversionService>();

WebApplication app = builder.Build();
app.UseCors("AllowAll");
app.MapControllers();
app.UseSwagger();
app.UseSwaggerUI();

app.MapGet("/ping", () => "pong");

app.Run();