using System.Text.Json.Serialization;
using Microsoft.EntityFrameworkCore;
using Minio;
using TagFilesService.Infrastructure;
using TagFilesService.Library;
using TagFilesService.Model;
using TagFilesService.WebHost;
using TagFilesService.WebHost.Options;

WebApplicationBuilder builder = WebApplication.CreateBuilder(args);
builder.Services
    .AddControllers()
    .AddJsonOptions(options => { options.JsonSerializerOptions.Converters.Add(new JsonStringEnumConverter()); });
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
    options.UseSqlite("Data Source=tag-files.db"));

S3Option? s3Options = builder.Configuration.GetSection(nameof(S3Option)).Get<S3Option>();
builder.Services.AddMinio(configure =>
{
    if (s3Options == null)
    {
        throw new ArgumentNullException(nameof(s3Options));
    }

    configure
        .WithEndpoint(s3Options!.Host)
        .WithCredentials(s3Options.AccessKey, s3Options.SecretKey)
        .WithSSL(false)
        .Build();
});
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